package messaging_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"keyloop-coding-challenge/package/eventsourcing"
)

// TestOutOfOrderVersionsRepairedFromEventStore: v3 arrives before v2 (e.g. across a
// re-snapshot boundary). v2 is read from the event store and applied first; the
// late v2 record is then a duplicate. Nothing is applied out of order.
func TestOutOfOrderVersionsRepairedFromEventStore(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	s := stream("v1", 1, 3)
	hist := &fakeHistory{}
	hist.store(s...)
	for _, e := range []storedEvent{s[0], s[2], s[1]} {
		tr.appendEvent(0, e)
	}
	r := start(t, tr, proj, hist, nil, testConfig())

	waitCommitted(t, tr, map[int32]int64{0: 3})
	if got := proj.history(); !reflect.DeepEqual(got, []string{"v1:1", "v1:2", "v1:3"}) {
		t.Fatalf("history %v", got)
	}
	st := r.consumer.Status()
	if st.GapEventsRepaired != 1 || st.DuplicatesSkipped != 1 || st.EventsApplied != 2 {
		t.Fatalf("status %+v", st)
	}
}

// TestFeedStartingMidStreamRepairedFromEventStore: the feed only has v4 onwards (topic
// retention, connector started after the history): v1..v3 come from the event store.
func TestFeedStartingMidStreamRepairedFromEventStore(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	s := stream("v1", 1, 5)
	hist := &fakeHistory{}
	hist.store(s...)
	tr.appendEvent(0, s[3])
	tr.appendEvent(0, s[4])
	start(t, tr, proj, hist, nil, testConfig())

	waitCommitted(t, tr, map[int32]int64{0: 2})
	if got := proj.history(); !reflect.DeepEqual(got, []string{"v1:1", "v1:2", "v1:3", "v1:4", "v1:5"}) {
		t.Fatalf("history %v", got)
	}
}

// TestUnrepairableGapHaltsOnlyItsPartition: versions missing from the feed AND the
// event store are never skipped. The partition stops at the record (offset not
// committed past it, readiness fails) while other partitions continue; once the
// history is complete the retry succeeds.
func TestUnrepairableGapHaltsOnlyItsPartition(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	a, b := stream("a", 1, 3), stream("b", 11, 2)
	hist := &fakeHistory{}
	hist.store(a[0], a[2]) // a v2 is missing everywhere
	tr.appendEvent(0, a[0])
	tr.appendEvent(0, a[2])
	tr.appendEvent(1, b[0])
	tr.appendEvent(1, b[1])
	r := start(t, tr, proj, hist, nil, testConfig())

	waitCommitted(t, tr, map[int32]int64{0: 1, 1: 2})
	eventually(t, "partition 0 halted", func() bool { return len(tr.retried()) >= 2 })
	if got := tr.committedOffset(0); got != 1 {
		t.Fatalf("partition 0 committed to %d past the gap", got)
	}
	if proj.version("a") != 1 {
		t.Fatalf("a applied to v%d across a gap", proj.version("a"))
	}
	err := r.consumer.Ready(context.Background())
	if err == nil || !strings.Contains(err.Error(), "halted at offset 1") || !strings.Contains(err.Error(), "cannot be repaired") {
		t.Fatalf("readiness: %v", err)
	}
	var p0 *struct{ poison, failing bool }
	for _, p := range r.consumer.Status().Partitions {
		if p.Partition == 0 {
			p0 = &struct{ poison, failing bool }{p.Poison, p.Failing}
		}
	}
	if p0 == nil || !p0.failing || !p0.poison {
		t.Fatalf("partition 0 status %+v", r.consumer.Status().Partitions)
	}

	hist.store(a[1]) // the operator restores the missing event
	waitCommitted(t, tr, map[int32]int64{0: 2})
	if proj.version("a") != 3 {
		t.Fatalf("a at v%d after repair", proj.version("a"))
	}
	eventually(t, "ready again", func() bool { return r.consumer.Ready(context.Background()) == nil })
}

// TestGapRepairCountedWhenTheRecordStillFails: versions repaired from the event store
// are committed even if the record that triggered the repair then fails; the counter
// says so.
func TestGapRepairCountedWhenTheRecordStillFails(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	s := stream("v1", 1, 3)
	hist := &fakeHistory{}
	hist.store(s...)
	tr.appendEvent(0, s[2])
	// v3 fails only once it is no longer a gap, i.e. after v1 and v2 were repaired.
	failing := &failAfterRepair{fakeProjector: proj}
	r := startWith(t, tr, failing, hist, testConfig())

	eventually(t, "v1 and v2 repaired", func() bool { return proj.version("v1") == 2 })
	eventually(t, "counter", func() bool { return r.consumer.Status().GapEventsRepaired == 2 })
	if tr.committedOffset(0) != 0 {
		t.Fatal("offset committed past the failing record")
	}
}

type failAfterRepair struct {
	*fakeProjector
}

func (p *failAfterRepair) Apply(ctx context.Context, ev eventsourcing.Event) (bool, error) {
	if ev.Version == 3 && p.version(ev.AggregateID) == 2 {
		return false, errors.New("deadlock")
	}
	return p.fakeProjector.Apply(ctx, ev)
}
