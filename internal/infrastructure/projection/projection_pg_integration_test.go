//go:build integration

package projection_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	ports "keyloop-coding-challenge/internal/application/projection"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/internal/testsupport/pgtest"
	"keyloop-coding-challenge/pkg/eventsourcing"

	"gorm.io/gorm"
)

var ctx = context.Background()

// stream builds the events of one vehicle with contiguous versions and event IDs.
type stream struct {
	id      string
	version int
	nextID  *int64
}

func newStream(id string, nextID *int64) *stream { return &stream{id: id, nextID: nextID} }

func (s *stream) ev(data interface{}) eventsourcing.Event {
	s.version++
	*s.nextID++
	return eventsourcing.Event{
		ID:            *s.nextID,
		AggregateID:   s.id,
		AggregateType: "VehicleInventoryAggregate",
		Version:       s.version,
		EventType:     fmt.Sprintf("%T", data)[8:],
		SchemaVersion: 1,
		Data:          data,
		Metadata: map[string]interface{}{
			"actor_id":       "manager-42",
			"correlation_id": fmt.Sprintf("req-%d", *s.nextID),
		},
		CreatedAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC).Unix() + int64(s.version)*3600,
	}
}

func stocked(dealer string, on domain.Date) *events.VehicleStockedEvent {
	return &events.VehicleStockedEvent{DealershipID: dealer, VIN: "1HGCM82633A004352", Make: "Toyota", Model: "Corolla", ModelYear: 2023, StockedOn: on}
}

func newPipeline(t *testing.T) (*gorm.DB, *viewstore.Postgres, projection.Projection) {
	t.Helper()
	db, _ := migratedDB(t)
	store := viewstore.NewPostgres(db)
	return db, store, projection.NewProjection(store, &projectors.VehicleProjector{})
}

func mustApply(t *testing.T, p projection.Projection, evs ...eventsourcing.Event) {
	t.Helper()
	for _, ev := range evs {
		if _, err := p.Apply(ctx, ev); err != nil {
			t.Fatalf("apply %s v%d: %v", ev.AggregateID, ev.Version, err)
		}
	}
}

type vehicleState struct {
	Status, ActionID, ActionType, ActionNote, ActionUpdatedBy string
	Version                                                   int
	SoldOn                                                    *time.Time
}

func vehicle(t *testing.T, db *gorm.DB, id string) *vehicleState {
	t.Helper()
	var rows []vehicleState
	if err := db.Raw(`SELECT status, coalesce(action_id,'') action_id, coalesce(action_type,'') action_type,
		coalesce(action_note,'') action_note, coalesce(action_updated_by,'') action_updated_by, version, sold_on
		FROM read_model.inventory_vehicle WHERE vehicle_id = ?`, id).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		return nil
	}
	return &rows[0]
}

type historyRow struct {
	Version                                                            int
	Change, ActionID, ActionType, Note, Reason, ActorID, CorrelationID string
}

func history(t *testing.T, db *gorm.DB, id string) []historyRow {
	t.Helper()
	var rows []historyRow
	if err := db.Raw(`SELECT version, change, action_id, action_type, note, reason, actor_id, correlation_id
		FROM read_model.vehicle_action_history WHERE vehicle_id = ? ORDER BY version`, id).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func appliedVersion(t *testing.T, db *gorm.DB, id string) int {
	t.Helper()
	var v []int
	if err := db.Raw(`SELECT version FROM read_model.projection_aggregate WHERE aggregate_id = ?`, id).Scan(&v).Error; err != nil {
		t.Fatal(err)
	}
	if len(v) == 0 {
		return 0
	}
	return v[0]
}

func TestProjectionLifecycle(t *testing.T) {
	db, _, p := newPipeline(t)
	var ids int64
	s := newStream("v1", &ids)

	mustApply(t, p, s.ev(stocked("d-001", domain.NewDate(2026, 7, 1))))
	if v := vehicle(t, db, "v1"); v == nil || v.Status != "IN_STOCK" || v.ActionID != "" || v.Version != 1 {
		t.Fatalf("after stock: %+v", v)
	}
	mustApply(t, p, s.ev(&events.VehicleActionProposedEvent{ActionID: "a1", ActionType: domain.ActionPriceReduction, Note: "5%"}))
	if v := vehicle(t, db, "v1"); v.ActionID != "a1" || v.ActionType != "PRICE_REDUCTION" || v.ActionUpdatedBy != "manager-42" {
		t.Fatalf("after propose: %+v", v)
	}
	mustApply(t, p, s.ev(&events.VehicleActionRevisedEvent{ActionID: "a1", ActionType: domain.ActionAuction, Note: "lot 7"}))
	if v := vehicle(t, db, "v1"); v.ActionID != "a1" || v.ActionType != "AUCTION" || v.ActionNote != "lot 7" {
		t.Fatalf("after revise: %+v", v)
	}
	mustApply(t, p, s.ev(&events.VehicleActionCancelledEvent{ActionID: "a1", Reason: "buyer found"}))
	if v := vehicle(t, db, "v1"); v.ActionID != "" || v.ActionType != "" {
		t.Fatalf("after cancel: %+v", v)
	}
	mustApply(t, p, s.ev(&events.VehicleActionProposedEvent{ActionID: "a2", ActionType: domain.ActionHold}))
	mustApply(t, p, s.ev(&events.VehicleSoldEvent{SoldOn: domain.NewDate(2026, 10, 20), ClosedActionID: "a2"}))
	v := vehicle(t, db, "v1")
	if v.Status != "SOLD" || v.SoldOn == nil || v.SoldOn.Format("2006-01-02") != "2026-10-20" || v.ActionID != "" || v.Version != 6 {
		t.Fatalf("after sale: %+v", v)
	}
	want := []historyRow{
		{2, "PROPOSED", "a1", "PRICE_REDUCTION", "5%", "", "manager-42", "req-2"},
		{3, "REVISED", "a1", "AUCTION", "lot 7", "", "manager-42", "req-3"},
		{4, "CANCELLED", "a1", "AUCTION", "lot 7", "buyer found", "manager-42", "req-4"},
		{5, "PROPOSED", "a2", "HOLD", "", "", "manager-42", "req-5"},
		{6, "CLOSED_BY_SALE", "a2", "HOLD", "", "", "manager-42", "req-6"},
	}
	if got := history(t, db, "v1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("history\n got %+v\nwant %+v", got, want)
	}
	if appliedVersion(t, db, "v1") != 6 {
		t.Fatal("applied version not recorded")
	}
}

func TestDuplicateEventIsHarmless(t *testing.T) {
	db, _, p := newPipeline(t)
	var ids int64
	s := newStream("v1", &ids)
	e1, e2 := s.ev(stocked("d-001", domain.NewDate(2026, 7, 1))), s.ev(&events.VehicleActionProposedEvent{ActionID: "a1", ActionType: domain.ActionHold})
	mustApply(t, p, e1, e2)
	for _, dup := range []eventsourcing.Event{e2, e1, e2} {
		applied, err := p.Apply(ctx, dup)
		if err != nil || applied {
			t.Fatalf("duplicate v%d: applied=%v err=%v", dup.Version, applied, err)
		}
	}
	if got := history(t, db, "v1"); len(got) != 1 {
		t.Fatalf("history duplicated: %+v", got)
	}
}

func TestVersionGapIsRejectedWithoutPartialCommit(t *testing.T) {
	db, _, p := newPipeline(t)
	var ids int64
	s := newStream("v1", &ids)
	mustApply(t, p, s.ev(stocked("d-001", domain.NewDate(2026, 7, 1))))
	s.ev(&events.VehicleActionProposedEvent{ActionID: "a1", ActionType: domain.ActionHold}) // v2 is "lost"
	v3 := s.ev(&events.VehicleActionRevisedEvent{ActionID: "a1", ActionType: domain.ActionAuction})
	if _, err := p.Apply(ctx, v3); !errors.Is(err, projection.ErrVersionGap) {
		t.Fatalf("gap: %v", err)
	}
	if v := vehicle(t, db, "v1"); v.Version != 1 || v.ActionID != "" || appliedVersion(t, db, "v1") != 1 || len(history(t, db, "v1")) != 0 {
		t.Fatalf("partial commit: %+v", v)
	}

	// A stream must start at version 1.
	other := newStream("v2", &ids)
	other.ev(stocked("d-001", domain.NewDate(2026, 7, 1)))
	if _, err := p.Apply(ctx, other.ev(&events.VehicleActionProposedEvent{ActionID: "a", ActionType: domain.ActionHold})); !errors.Is(err, projection.ErrVersionGap) {
		t.Fatalf("missing v1: %v", err)
	}
	if vehicle(t, db, "v2") != nil {
		t.Fatal("row created without version 1")
	}
}

func TestUnknownOrInconsistentEventsFailSafely(t *testing.T) {
	db, _, p := newPipeline(t)
	var ids int64
	s := newStream("v1", &ids)
	mustApply(t, p, s.ev(stocked("d-001", domain.NewDate(2026, 7, 1))))

	unknown := eventsourcing.Event{ID: 99, AggregateID: "v1", Version: 2, EventType: "VehicleTeleportedEvent", Data: &struct{}{}}
	if _, err := p.Apply(ctx, unknown); !errors.Is(err, projectors.ErrUnsupportedEvent) {
		t.Fatalf("unknown type: %v", err)
	}
	newer := s.ev(&events.VehicleActionProposedEvent{ActionID: "a1", ActionType: domain.ActionHold})
	newer.SchemaVersion = 2
	if _, err := p.Apply(ctx, newer); !errors.Is(err, projection.ErrUnsupportedSchemaVersion) {
		t.Fatalf("schema v2: %v", err)
	}
	wrongAction := newer
	wrongAction.SchemaVersion = 1
	wrongAction.Data = &events.VehicleActionRevisedEvent{ActionID: "nope", ActionType: domain.ActionHold}
	if _, err := p.Apply(ctx, wrongAction); !errors.Is(err, projectors.ErrInconsistent) {
		t.Fatalf("inconsistent: %v", err)
	}
	if appliedVersion(t, db, "v1") != 1 || vehicle(t, db, "v1").Version != 1 || len(history(t, db, "v1")) != 0 {
		t.Fatal("a failed event left partial state")
	}
}

func TestConcurrentDeliveryOfFirstAndLaterEvents(t *testing.T) {
	db, _, p := newPipeline(t)
	const deliveries = 16
	var ids int64
	s := newStream("v1", &ids)
	first := s.ev(stocked("d-001", domain.NewDate(2026, 7, 1)))
	second := s.ev(&events.VehicleActionProposedEvent{ActionID: "a1", ActionType: domain.ActionHold})

	for _, ev := range []eventsourcing.Event{first, second} {
		start := make(chan struct{})
		results := make([]bool, deliveries)
		errs := make([]error, deliveries)
		var wg sync.WaitGroup
		for i := 0; i < deliveries; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results[i], errs[i] = p.Apply(ctx, ev)
			}()
		}
		close(start)
		wg.Wait()
		appliedCount := 0
		for i := range results {
			if errs[i] != nil {
				t.Fatalf("v%d delivery %d: %v", ev.Version, i, errs[i])
			}
			if results[i] {
				appliedCount++
			}
		}
		if appliedCount != 1 {
			t.Fatalf("v%d applied %d times", ev.Version, appliedCount)
		}
	}
	var n int64
	db.Raw(`SELECT count(*) FROM read_model.inventory_vehicle`).Scan(&n)
	if n != 1 || len(history(t, db, "v1")) != 1 || appliedVersion(t, db, "v1") != 2 {
		t.Fatalf("rows=%d history=%d", n, len(history(t, db, "v1")))
	}
}

// source binds a fixed history to any run transaction, like repos.NewEventStore(tx, …).
func source(events []eventsourcing.Event) func(*gorm.DB) projection.EventSource {
	return func(*gorm.DB) projection.EventSource { return fakeSource{events: events} }
}

// fakeSource replays a fixed history like repos.EventStore.ReadAll.
type fakeSource struct{ events []eventsourcing.Event }

func (f fakeSource) ReadAll(_ context.Context, _ int, fn func(eventsourcing.Event) error) error {
	for _, ev := range f.events {
		if err := fn(ev); err != nil {
			return err
		}
	}
	return nil
}

// history100 is a deterministic multi-vehicle history.
func historyFor(vehicles int) []eventsourcing.Event {
	var ids int64
	var out []eventsourcing.Event
	for i := 0; i < vehicles; i++ {
		s := newStream(fmt.Sprintf("veh-%03d", i), &ids)
		out = append(out, s.ev(stocked([]string{"d-001", "d-002"}[i%2], domain.NewDate(2026, 6, 1).AddDays(i))))
		if i%3 != 0 {
			out = append(out, s.ev(&events.VehicleActionProposedEvent{ActionID: fmt.Sprintf("a-%d", i), ActionType: domain.ActionHold, Note: "n"}))
		}
		if i%3 == 2 {
			out = append(out, s.ev(&events.VehicleActionRevisedEvent{ActionID: fmt.Sprintf("a-%d", i), ActionType: domain.ActionAuction, Note: "r"}))
		}
		if i%5 == 4 {
			closed := ""
			if i%3 != 0 {
				closed = fmt.Sprintf("a-%d", i)
			}
			out = append(out, s.ev(&events.VehicleSoldEvent{SoldOn: domain.NewDate(2026, 10, 1), ClosedActionID: closed}))
		}
	}
	return out
}

func dump(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var vehicles, histories []map[string]interface{}
	if err := db.Raw(`SELECT * FROM read_model.inventory_vehicle ORDER BY vehicle_id`).Scan(&vehicles).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT * FROM read_model.vehicle_action_history ORDER BY vehicle_id, version`).Scan(&histories).Error; err != nil {
		t.Fatal(err)
	}
	var applied []map[string]interface{}
	if err := db.Raw(`SELECT aggregate_id, version, last_event_id FROM read_model.projection_aggregate ORDER BY aggregate_id`).Scan(&applied).Error; err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(vehicles, histories, applied)
}

func TestRebuildEqualsIncrementalAndIsRepeatable(t *testing.T) {
	evs := historyFor(60)

	liveDB, _, live := newPipeline(t)
	mustApply(t, live, evs...)

	rebuiltDB, _ := migratedDB(t)
	runner := projection.NewRunner(source(evs), viewstore.NewPostgres(rebuiltDB), &projectors.VehicleProjector{})
	res, err := runner.Rebuild(ctx)
	if err != nil || res.Read != len(evs) || res.Applied != len(evs) {
		t.Fatalf("rebuild: %+v %v", res, err)
	}
	want := dump(t, liveDB)
	if got := dump(t, rebuiltDB); got != want {
		t.Fatalf("rebuilt read model differs from incremental projection")
	}
	// Repeatable: a second rebuild gives the same state.
	if res, err := runner.Rebuild(ctx); err != nil || res.Applied != len(evs) {
		t.Fatalf("second rebuild: %+v %v", res, err)
	}
	if got := dump(t, rebuiltDB); got != want {
		t.Fatal("second rebuild changed the read model")
	}
	var runs int64
	rebuiltDB.Raw(`SELECT count(*) FROM read_model.projection_run`).Scan(&runs)
	if runs != 2 {
		t.Fatalf("runs recorded: %d", runs)
	}
}

func TestFailedRunRollsBackEverything(t *testing.T) {
	db, _ := migratedDB(t)
	store := viewstore.NewPostgres(db)
	good := historyFor(5)
	if _, err := projection.NewRunner(source(good), store, &projectors.VehicleProjector{}).Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	before := dump(t, db)

	bad := append(append([]eventsourcing.Event{}, historyFor(8)...), eventsourcing.Event{
		ID: 999, AggregateID: "veh-000", Version: 99, EventType: "X", Data: &struct{}{}}) // gap at the very end
	if _, err := projection.NewRunner(source(bad), store, &projectors.VehicleProjector{}).Rebuild(ctx); !errors.Is(err, projection.ErrVersionGap) {
		t.Fatalf("got %v", err)
	}
	if after := dump(t, db); after != before {
		t.Fatal("a failed rebuild changed the read model (truncate or partial events leaked)")
	}
}

func TestConcurrentRunsAreRejected(t *testing.T) {
	db, _ := migratedDB(t)
	store := viewstore.NewPostgres(db)
	holding, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, _, err := store.RunExclusive(ctx, "rebuild", true, func(*viewstore.Postgres) (int, int, error) {
			close(holding)
			<-release
			return 0, 0, nil
		})
		done <- err
	}()
	<-holding
	// Bounded: without the run lock the second run would block on the first one's
	// TRUNCATE instead of failing fast.
	secondCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := projection.NewRunner(source(historyFor(2)), store, &projectors.VehicleProjector{}).Rebuild(secondCtx); !errors.Is(err, viewstore.ErrRunInProgress) {
		t.Fatalf("second run: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func migratedDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	db, dsn := pgtest.NewDatabase(t)
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := viewstore.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db, dsn
}

// TestRunAndAppliersExcludeEachOther covers the shared/exclusive projection lock: a
// per-event applier waits while a run holds it, and a run is refused while an applier
// is mid-transaction.
func TestRunAndAppliersExcludeEachOther(t *testing.T) {
	db, _ := migratedDB(t)
	store := viewstore.NewPostgres(db)
	p := projection.NewProjection(store, &projectors.VehicleProjector{})
	var ids int64
	first := newStream("v1", &ids).ev(stocked("d-001", domain.NewDate(2026, 7, 1)))

	// 1. A run holds the lock: an applier cannot proceed until it ends.
	holding, release := make(chan struct{}), make(chan struct{})
	runDone := make(chan error, 1)
	go func() {
		_, _, err := store.RunExclusive(ctx, "sync", false, func(*viewstore.Postgres) (int, int, error) {
			close(holding)
			<-release
			return 0, 0, nil
		})
		runDone <- err
	}()
	<-holding
	blocked, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if _, err := p.Apply(blocked, first); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("applier during a run: %v (want it to wait)", err)
	}
	close(release)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if applied, err := p.Apply(ctx, first); err != nil || !applied {
		t.Fatalf("applier after the run: %v %v", applied, err)
	}

	// 2. An applier is mid-transaction: a run is refused immediately.
	inTx, done := make(chan struct{}), make(chan struct{})
	applierDone := make(chan error, 1)
	go func() {
		applierDone <- store.WithTransaction(ctx, func(tx ports.Store) error {
			if _, err := tx.LockAggregate(ctx, "v2"); err != nil {
				return err
			}
			close(inTx)
			<-done
			return nil
		})
	}()
	<-inTx
	if _, _, err := store.RunExclusive(ctx, "sync", false, func(*viewstore.Postgres) (int, int, error) { return 0, 0, nil }); !errors.Is(err, viewstore.ErrRunInProgress) {
		t.Fatalf("run during an applier: %v", err)
	}
	close(done)
	if err := <-applierDone; err != nil {
		t.Fatal(err)
	}
}
