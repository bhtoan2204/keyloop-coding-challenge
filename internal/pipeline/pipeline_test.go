//go:build integration && pipeline

package pipeline_test

import (
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/application/messaging"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
)

const converge = 90 * time.Second

// TestPipelineLiveCommandsReachReadModel: commands become read-model rows through
// Debezium and Kafka without any manual sync, each event applied once, and the
// group's committed offsets reach the end of every partition.
func TestPipelineLiveCommandsReachReadModel(t *testing.T) {
	e := newEnv(t)
	e.registerConnector(t)
	c := e.startConsumer(t, consumerOpts{startupSync: true})
	w := newWorkload(e)
	// Commands written during the startup sync may be applied by it (and then skipped
	// as duplicates when they arrive from Kafka); wait so that every event below
	// travels through the change feed.
	waitFor(t, "consumer bootstrapped", converge, func() bool { return c.consumer.Status().Phase == messaging.PhaseConsuming })

	// One command, timed from commit to visibility in PostgreSQL.
	started := time.Now()
	w.advance(t, []string{"lag-probe"}, 1)
	waitFor(t, "first event projected", converge, func() bool { return e.projectedVersions(t)["lag-probe"] == 1 })
	t.Logf("single-event end-to-end lag (command return → row in PostgreSQL): %s", time.Since(started).Round(time.Millisecond))

	w.advance(t, vehicleIDs("live", 5), lifecycleSteps)
	e.waitProjected(t, converge)
	e.assertMatchesRebuild(t)

	waitFor(t, "offsets committed to the end of the topic", converge, func() bool {
		return reflect.DeepEqual(e.committedOffsets(t), e.topicEndOffsets(t))
	})
	st := c.consumer.Status()
	if st.EventsApplied != int64(w.count()) || st.DuplicatesSkipped != 0 || st.GapEventsRepaired != 0 || !st.Ready {
		t.Fatalf("consumer status after %d commands: %+v", w.count(), st)
	}
}

// TestPipelineInitialSnapshotWithExistingHistoryAndProjections: the connector starts
// after history exists and part of it is already projected. Its snapshot delivers
// everything; already-projected events are skipped, the rest applied. The consumer
// runs WITHOUT the startup sync, so the Debezium snapshot alone must suffice.
func TestPipelineInitialSnapshotWithExistingHistoryAndProjections(t *testing.T) {
	e := newEnv(t)
	w := newWorkload(e)
	vehicles := vehicleIDs("old", 6)
	w.advance(t, vehicles, 3)
	runner := projection.NewRunner(e.events, viewstore.NewPostgres(e.pg), &projectors.VehicleProjector{})
	synced, err := runner.Sync(ctx)
	if err != nil || synced.Applied != 18 {
		t.Fatalf("pre-existing projection: %+v %v", synced, err)
	}
	w.advance(t, vehicles, 3) // history the read model has not seen

	e.registerConnector(t)
	c := e.startConsumer(t, consumerOpts{startupSync: false})
	e.waitProjected(t, converge)
	e.assertMatchesRebuild(t)
	if st := c.consumer.Status(); st.DuplicatesSkipped != 18 || st.EventsApplied != 18 {
		t.Fatalf("snapshot should re-deliver 18 projected and 18 new events: %+v", st)
	}
}

// TestPipelineBootstrapWhileCommandsAreWritten: the connector snapshots and the
// consumer bootstraps (startup sync) while commands keep committing. Whatever lands
// on either side of the snapshot/stream boundary and of the startup sync is applied
// exactly once.
func TestPipelineBootstrapWhileCommandsAreWritten(t *testing.T) {
	e := newEnv(t)
	w := newWorkload(e)
	w.advance(t, vehicleIDs("pre", 4), 2)

	stop := w.background("busy", 8)
	before := w.count()
	e.registerConnector(t)
	afterConnector := w.count()
	c := e.startConsumer(t, consumerOpts{startupSync: true})
	waitFor(t, "consumer bootstrapped", converge, func() bool { return c.consumer.Status().Phase == messaging.PhaseConsuming })
	afterBootstrap := w.count()
	waitFor(t, "more commands after bootstrap", converge, func() bool { return w.count() >= afterBootstrap+60 })
	issued, err := stop()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("commands: %d before the connector, %d during registration/snapshot, %d during bootstrap, %d total",
		before, afterConnector-before, afterBootstrap-afterConnector, issued)

	e.waitProjected(t, converge)
	e.assertMatchesRebuild(t)
	t.Logf("consumer status: %+v", c.consumer.Status())
}

// TestPipelineConsumerCrashAndRestart: a consumer whose offset commits are all lost
// (crash after each read-model commit) applies everything; a restarted member
// re-reads it all and skips it by version, then applies what was written while no
// consumer ran. No history is duplicated and nothing is lost.
func TestPipelineConsumerCrashAndRestart(t *testing.T) {
	e := newEnv(t)
	e.registerConnector(t)
	w := newWorkload(e)
	vehicles := vehicleIDs("crash", 4)

	first := e.startConsumer(t, consumerOpts{
		wrap: func(tr messaging.Transport) messaging.Transport { return commitFails{tr} },
	})
	w.advance(t, vehicles, 3)
	e.waitProjected(t, converge)
	first.stop(t)
	if got := e.committedOffsets(t); len(got) != 0 {
		t.Fatalf("offsets committed although every commit failed: %v", got)
	}

	w.advance(t, vehicles, 3) // written while no consumer runs
	second := e.startConsumer(t, consumerOpts{})
	e.waitProjected(t, converge)
	e.assertMatchesRebuild(t)
	st := second.consumer.Status()
	if st.DuplicatesSkipped != 12 || st.EventsApplied != 12 {
		t.Fatalf("restarted consumer should skip 12 re-delivered and apply 12 new events: %+v", st)
	}
	if got := e.historyVersions(t, "crash-0"); !reflect.DeepEqual(got, []int{2, 3, 4, 5, 6}) {
		t.Fatalf("history of crash-0: versions %v", got)
	}
}

// TestPipelineProjectionFailureRollsBackAndHalts: a failure inside the read-model
// transaction rolls back everything the event wrote, the partition stops at the
// record (offset not committed, readiness failing), and once the cause is gone the
// event is applied exactly once. The failing event is its vehicle's LAST one, so a
// transport that skipped it could not be rescued by gap repair from a later event.
func TestPipelineProjectionFailureRollsBackAndHalts(t *testing.T) {
	e := newEnv(t)
	e.registerConnector(t)
	failing := &failingProjector{
		aggregateID: "fail-0",
		version:     3,
	}
	failing.failing.Store(true)
	c := e.startConsumer(t, consumerOpts{extra: []projectors.Projector{failing}})
	w := newWorkload(e)
	w.advance(t, vehicleIDs("fail", 3), 3)

	waitFor(t, "repeated failures", converge, func() bool { return failing.failures.Load() >= 3 })
	if v := e.projectedVersions(t)["fail-0"]; v != 2 {
		t.Fatalf("fail-0 projected at v%d while v3 fails", v)
	}
	if got := e.historyVersions(t, "fail-0"); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("history of fail-0 %v: the rolled-back v3 must not be there", got)
	}
	var vehicle struct {
		Version    int
		ActionType string
	}
	if err := e.pg.Raw(`SELECT version, action_type FROM inventory_vehicle WHERE vehicle_id = 'fail-0'`).Scan(&vehicle).Error; err != nil ||
		vehicle.Version != 2 || vehicle.ActionType != "HOLD" {
		t.Fatalf("inventory_vehicle of fail-0: %+v (%v); want version 2, the revision to AUCTION rolled back", vehicle, err)
	}
	if err := c.consumer.Ready(ctx); err == nil {
		t.Fatal("ready while a partition is halted")
	}

	failing.failing.Store(false)
	e.waitProjected(t, converge)
	e.assertMatchesRebuild(t)
	waitFor(t, "ready again", converge, func() bool { return c.consumer.Ready(ctx) == nil })
}

// TestPipelineConnectorRestart: restarting, pausing (while commands are written) and
// re-creating the connector resumes from its stored binlog offset; any replayed
// change is skipped by version.
func TestPipelineConnectorRestart(t *testing.T) {
	e := newEnv(t)
	e.registerConnector(t)
	c := e.startConsumer(t, consumerOpts{startupSync: true})
	w := newWorkload(e)
	vehicles := vehicleIDs("conn", 4)

	w.advance(t, vehicles, 1)
	e.waitProjected(t, converge)

	e.connect(t, http.MethodPost, "/connectors/"+e.connector+"/restart?includeTasks=true", nil, http.StatusOK, http.StatusAccepted, http.StatusNoContent)
	e.waitConnectorRunning(t)
	w.advance(t, vehicles, 1)
	e.waitProjected(t, converge)

	e.connect(t, http.MethodPut, "/connectors/"+e.connector+"/pause", nil, http.StatusAccepted)
	w.advance(t, vehicles, 2) // written while the connector is paused
	e.connect(t, http.MethodPut, "/connectors/"+e.connector+"/resume", nil, http.StatusAccepted)
	e.waitConnectorRunning(t)
	e.waitProjected(t, converge)

	e.connect(t, http.MethodDelete, "/connectors/"+e.connector, nil, http.StatusNoContent)
	w.advance(t, vehicles, 1) // written while no connector exists
	e.registerConnector(t)    // same name: resumes from its stored offset
	w.advance(t, vehicles, 1)
	e.waitProjected(t, converge)
	e.assertMatchesRebuild(t)
	t.Logf("consumer status: %+v", c.consumer.Status())
}

// TestPipelineRebuildDuringLiveConsumption: a rebuild from MySQL while the consumer
// runs and commands are written. The projection lock orders the two; the live
// consumer continues after the rebuild and the result equals a rebuild.
func TestPipelineRebuildDuringLiveConsumption(t *testing.T) {
	e := newEnv(t)
	e.registerConnector(t)
	e.startConsumer(t, consumerOpts{startupSync: true})
	w := newWorkload(e)
	stop := w.background("rebuild", 6)
	waitFor(t, "traffic", converge, func() bool { return w.count() >= 40 })

	runner := projection.NewRunner(e.events, viewstore.NewPostgres(e.pg), &projectors.VehicleProjector{})
	attempts := 0
	waitFor(t, "rebuild to get the projection lock", converge, func() bool {
		attempts++
		res, err := runner.Rebuild(ctx)
		if errors.Is(err, viewstore.ErrRunInProgress) {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("rebuild after %d attempts: read %d, applied %d", attempts, res.Read, res.Applied)
		return true
	})
	afterRebuild := w.count()
	waitFor(t, "traffic after the rebuild", converge, func() bool { return w.count() >= afterRebuild+30 })
	if _, err := stop(); err != nil {
		t.Fatal(err)
	}
	e.waitProjected(t, converge)
	e.assertMatchesRebuild(t)
}

// TestPipelineMultiplePartitionsAndMembers: two members of one group share the three
// partitions; per-aggregate order holds (no gap had to be repaired, nothing halted)
// and every event is applied exactly once across both members.
func TestPipelineMultiplePartitionsAndMembers(t *testing.T) {
	e := newEnv(t)
	e.registerConnector(t)
	a := e.startConsumer(t, consumerOpts{startupSync: true})
	b := e.startConsumer(t, consumerOpts{startupSync: true})
	waitFor(t, "both members bootstrapped", converge, func() bool {
		return a.consumer.Status().Phase == messaging.PhaseConsuming && b.consumer.Status().Phase == messaging.PhaseConsuming
	})
	// The connector creates the topic with its first record; members join the group
	// once the topic exists.
	w := newWorkload(e)
	w.advance(t, []string{"multi-first"}, 1)
	e.waitProjected(t, converge)
	e.waitGroupMembers(t, 2)

	w.advance(t, vehicleIDs("multi", 30), lifecycleSteps)
	e.waitProjected(t, converge)
	e.assertMatchesRebuild(t)

	sa, sb := a.consumer.Status(), b.consumer.Status()
	t.Logf("member a: applied %d, partitions %+v", sa.EventsApplied, sa.Partitions)
	t.Logf("member b: applied %d, partitions %+v", sb.EventsApplied, sb.Partitions)
	if sa.EventsApplied+sb.EventsApplied != int64(w.count()) {
		t.Fatalf("applied %d + %d events for %d commands", sa.EventsApplied, sb.EventsApplied, w.count())
	}
	if sa.EventsApplied == 0 || sb.EventsApplied == 0 {
		t.Fatal("one member applied nothing: the partitions were not shared")
	}
	if sa.GapEventsRepaired+sb.GapEventsRepaired != 0 || !sa.Ready || !sb.Ready {
		t.Fatalf("gaps repaired or not ready: %+v / %+v", sa, sb)
	}
}

// TestPipelineTruncateOfEventTableHalts: es_event is append-only. A TRUNCATE (with
// skipped.operations=none the connector emits it) must stop the projector, never be
// ignored while the read model keeps state the event store no longer has.
func TestPipelineTruncateOfEventTableHalts(t *testing.T) {
	e := newEnv(t)
	e.registerConnector(t)
	c := e.startConsumer(t, consumerOpts{startupSync: true})
	w := newWorkload(e)
	w.advance(t, vehicleIDs("trunc", 2), 1)
	e.waitProjected(t, converge)

	if err := e.mysql.Exec("TRUNCATE TABLE es_event").Error; err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a partition halted on the truncate", converge, func() bool {
		for _, p := range c.consumer.Status().Partitions {
			if p.Failing && p.Poison {
				t.Logf("halted: %s[%d]@%d: %s", p.Topic, p.Partition, p.FailingOffset, p.LastError)
				return true
			}
		}
		return false
	})
	if err := c.consumer.Ready(ctx); err == nil {
		t.Fatal("ready after the event table was truncated")
	}
	if got := e.projectedVersions(t); len(got) != 2 {
		t.Fatalf("read model changed by the truncate: %v", got)
	}
}
