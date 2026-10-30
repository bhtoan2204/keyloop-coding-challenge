//go:build integration

package projection_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/pkg/eventsourcing"
)

var businessDate = domain.NewDate(2026, 10, 29)

// commandStore is the API's write path: the event store with the read model projected
// in the same transaction.
func commandStore(t *testing.T, db *gorm.DB) (eventstore.AggregateStore, eventsourcing.Serializer) {
	t.Helper()
	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&aggregates.VehicleInventoryAggregate{}); err != nil {
		t.Fatal(err)
	}
	store := eventstore.NewAggregateStoreWithSnapshotInterval(
		repos.New(db, serializer),
		0,
		eventstore.WithProjection(projection.NewCommandProjection(&projectors.VehicleProjector{})),
	)
	return store, serializer
}

func stockedVehicle(t *testing.T, id string) *aggregates.VehicleInventoryAggregate {
	t.Helper()
	v := &aggregates.VehicleInventoryAggregate{}
	if err := v.SetID(id); err != nil {
		t.Fatal(err)
	}
	err := v.Stock(aggregates.StockVehicle{
		DealershipID: "d-001",
		VIN:          "1HGCM82633A004352",
		Make:         "Toyota",
		Model:        "Corolla",
		ModelYear:    2023,
		StockedOn:    businessDate.AddDays(-120),
	}, businessDate)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func propose(t *testing.T, v *aggregates.VehicleInventoryAggregate, actionID string) {
	t.Helper()
	cmd := aggregates.ProposeAction{
		ActionID:   actionID,
		ActionType: domain.ActionHold,
	}
	if err := v.ProposeAction(cmd, businessDate); err != nil {
		t.Fatal(err)
	}
}

func storedVersion(t *testing.T, db *gorm.DB, id string) (aggregate int, events int) {
	t.Helper()
	if err := db.Raw(`SELECT COALESCE((SELECT version FROM event_store.es_aggregate WHERE id = ?), 0)`, id).Scan(&aggregate).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT count(*) FROM event_store.es_event WHERE aggregate_id = ?`, id).Scan(&events).Error; err != nil {
		t.Fatal(err)
	}
	return aggregate, events
}

// TestProjectionFailureRollsBackTheEvents: a read-model write that fails inside the save
// (here a trigger on the history table) fails the whole save: no event, no CAS, no
// read-model row, and the aggregate keeps its pending events so the caller can retry.
func TestProjectionFailureRollsBackTheEvents(t *testing.T) {
	db, _ := migratedDB(t)
	store, _ := commandStore(t, db)
	v := stockedVehicle(t, "v1")
	if err := store.Save(ctx, v); err != nil {
		t.Fatal(err)
	}
	before := dump(t, db)

	mustExecSQL(t, db, `
		CREATE FUNCTION read_model.fail_history() RETURNS trigger LANGUAGE plpgsql AS
			$$ BEGIN RAISE EXCEPTION 'history write refused'; END $$;
		CREATE TRIGGER fail_history BEFORE INSERT ON read_model.vehicle_action_history
			FOR EACH ROW EXECUTE FUNCTION read_model.fail_history();`)
	propose(t, v, "a1")
	if err := store.Save(ctx, v); err == nil {
		t.Fatal("save succeeded although the projection failed")
	}
	if a, n := storedVersion(t, db, "v1"); a != 1 || n != 1 {
		t.Fatalf("event store after the failed save: aggregate version %d, %d events", a, n)
	}
	if after := dump(t, db); after != before {
		t.Fatalf("read model changed by a failed save\nbefore %s\nafter  %s", before, after)
	}
	if v.Version() != 2 || len(v.Events()) != 1 {
		t.Fatalf("pending events were acknowledged: version %d, pending %d", v.Version(), len(v.Events()))
	}

	mustExecSQL(t, db, `DROP TRIGGER fail_history ON read_model.vehicle_action_history`)
	if err := store.Save(ctx, v); err != nil {
		t.Fatalf("retry after the failure: %v", err)
	}
	if a, n := storedVersion(t, db, "v1"); a != 2 || n != 2 || appliedVersion(t, db, "v1") != 2 || len(history(t, db, "v1")) != 1 {
		t.Fatalf("after retry: aggregate %d events %d projected %d", a, n, appliedVersion(t, db, "v1"))
	}
}

// TestReadModelAheadOfEventStoreFailsTheSave: if the read model already has the version
// a command is about to append (restored separately, say), the save fails rather than
// committing an event the read model silently ignores.
func TestReadModelAheadOfEventStoreFailsTheSave(t *testing.T) {
	db, _ := migratedDB(t)
	store, _ := commandStore(t, db)
	var ids int64 = 1000
	mustApply(t, projection.NewProjection(viewstore.NewPostgres(db), &projectors.VehicleProjector{}),
		newStream("v1", &ids).ev(stocked("d-001", businessDate.AddDays(-120))))

	err := store.Save(ctx, stockedVehicle(t, "v1"))
	if !errors.Is(err, projection.ErrVersionGap) {
		t.Fatalf("got %v", err)
	}
	if a, n := storedVersion(t, db, "v1"); a != 0 || n != 0 {
		t.Fatalf("event store after the refused save: aggregate version %d, %d events", a, n)
	}
}

// TestRebuildWhileCommandsRun: commands and rebuilds interleave freely. A rebuild either
// runs (commands wait on the projection lock) or is refused while a command holds it;
// every committed event is projected exactly once and a final rebuild changes nothing.
func TestRebuildWhileCommandsRun(t *testing.T) {
	db, _ := migratedDB(t)
	store, serializer := commandStore(t, db)
	runner := projection.NewRunner(
		func(tx *gorm.DB) projection.EventSource { return repos.NewEventStore(tx, serializer) },
		viewstore.NewPostgres(db),
		&projectors.VehicleProjector{},
	)

	const writers, perWriter = 4, 10
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter*2+100)
	stop := make(chan struct{})
	rebuilt := 0
	rebuilderDone := make(chan struct{})
	go func() {
		defer close(rebuilderDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, err := runner.Rebuild(ctx)
			switch {
			case err == nil:
				rebuilt++
			case !errors.Is(err, viewstore.ErrRunInProgress):
				errs <- fmt.Errorf("rebuild: %w", err)
				return
			}
		}
	}()
	for w := 0; w < writers; w++ {
		vehicles := make([]*aggregates.VehicleInventoryAggregate, perWriter)
		for i := range vehicles {
			vehicles[i] = stockedVehicle(t, fmt.Sprintf("w%d-%02d", w, i))
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, v := range vehicles {
				if err := store.Save(ctx, v); err != nil {
					errs <- err
					return
				}
				cmd := aggregates.ProposeAction{
					ActionID:   "a1",
					ActionType: domain.ActionHold,
				}
				if err := v.ProposeAction(cmd, businessDate); err != nil {
					errs <- err
					return
				}
				if err := store.Save(ctx, v); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	<-rebuilderDone
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	t.Logf("%d rebuilds completed during the commands", rebuilt)

	var vehicles, events, projected int
	db.Raw(`SELECT count(*) FROM read_model.inventory_vehicle`).Scan(&vehicles)
	db.Raw(`SELECT count(*) FROM event_store.es_event`).Scan(&events)
	db.Raw(`SELECT COALESCE(sum(version), 0) FROM read_model.projection_aggregate`).Scan(&projected)
	if vehicles != writers*perWriter || events != 2*writers*perWriter || projected != events {
		t.Fatalf("vehicles %d, events %d, projected versions %d", vehicles, events, projected)
	}
	before := dump(t, db)
	if _, err := runner.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	if after := dump(t, db); after != before {
		t.Fatal("a final rebuild changed the read model built by the interleaved commands")
	}
}

func mustExecSQL(t *testing.T, db *gorm.DB, sql string) {
	t.Helper()
	if err := db.Exec(sql).Error; err != nil {
		t.Fatal(err)
	}
}
