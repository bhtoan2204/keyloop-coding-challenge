package eventstore_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/gorm"
)

const vehicleID = "0192c6d2-0000-7000-8000-00000000e2e1"

var businessDate = domain.NewDate(2026, 10, 29)

func newVehicleStore(t *testing.T, interval int) (*gorm.DB, eventstore.AggregateStore) {
	t.Helper()
	db := openDB(t)
	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&aggregates.VehicleInventoryAggregate{}); err != nil {
		t.Fatal(err)
	}
	return db, newStoreWith(t, db, serializer, interval)
}

func loadVehicle(t *testing.T, store eventstore.AggregateStore) *aggregates.VehicleInventoryAggregate {
	t.Helper()
	v := &aggregates.VehicleInventoryAggregate{}
	if err := store.Get(context.Background(), vehicleID, v); err != nil {
		t.Fatal(err)
	}
	return v
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// stateJSON is the aggregate's complete business state (its snapshot JSON, which a
// domain test proves covers every state field).
func stateJSON(t *testing.T, v *aggregates.VehicleInventoryAggregate) []byte {
	t.Helper()
	b, err := v.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertSameVehicle(t *testing.T, got, want *aggregates.VehicleInventoryAggregate) {
	t.Helper()
	if g, w := stateJSON(t, got), stateJSON(t, want); !bytes.Equal(g, w) {
		t.Fatalf("state mismatch\n got: %s\nwant: %s", g, w)
	}
	if got.Version() != want.Version() || got.IsUnsaved() {
		t.Fatalf("version %d (unsaved=%v), want %d", got.Version(), got.IsUnsaved(), want.Version())
	}
}

// TestVehicleLifecycleReplayFromStore uses the runtime configuration (snapshots off):
// every load replays the full stream.
func TestVehicleLifecycleReplayFromStore(t *testing.T) {
	db, store := newVehicleStore(t, 0)
	ctx := context.Background()

	v := &aggregates.VehicleInventoryAggregate{}
	must(t, v.SetID(vehicleID))
	must(t, v.Stock(aggregates.StockVehicle{
		DealershipID: "d-001", VIN: "1HGCM82633A004352", Make: "Toyota", Model: "Corolla",
		ModelYear: 2023, StockedOn: businessDate.AddDays(-120),
	}, businessDate))
	must(t, store.Save(ctx, v))

	// Two events in one save.
	must(t, v.ProposeAction(aggregates.ProposeAction{ActionID: "a1", ActionType: domain.ActionPriceReduction, Note: "5%"}, businessDate))
	must(t, v.ReviseAction(aggregates.ReviseAction{ActionID: "a1", ActionType: domain.ActionAuction, Note: "auction"}))
	must(t, store.Save(ctx, v))
	assertSameVehicle(t, loadVehicle(t, store), v)

	must(t, v.CancelAction(aggregates.CancelAction{ActionID: "a1", Reason: "offer received"}))
	must(t, v.ProposeAction(aggregates.ProposeAction{ActionID: "a2", ActionType: domain.ActionHold}, businessDate))
	must(t, store.Save(ctx, v))

	loaded := loadVehicle(t, store)
	assertSameVehicle(t, loaded, v)
	if a, ok := loaded.ActiveAction(); !ok || a.ID != "a2" || !loaded.IsAging(businessDate) {
		t.Fatalf("active=%+v aging=%v", a, loaded.IsAging(businessDate))
	}

	// The reloaded aggregate keeps enforcing invariants and continues the stream.
	err := loaded.ProposeAction(aggregates.ProposeAction{ActionID: "a3", ActionType: domain.ActionHold}, businessDate)
	if !errors.Is(err, domain.ErrActionAlreadyActive) {
		t.Fatalf("got %v", err)
	}
	must(t, loaded.Sell(aggregates.SellVehicle{SoldOn: businessDate}, businessDate))
	must(t, store.Save(ctx, loaded))

	final := loadVehicle(t, store)
	assertSameVehicle(t, final, loaded)
	if final.Status() != domain.StatusSold || final.Version() != 6 {
		t.Fatalf("status=%s version=%d", final.Status(), final.Version())
	}
	if got := eventStreamColumns(t, db, vehicleID); len(got) != 6 || got[5] != (streamColumns{6, "VehicleInventoryAggregate", 1}) {
		t.Fatalf("stream columns %+v", got)
	}
	var snapshots int64
	must(t, db.Raw(`SELECT COUNT(*) FROM event_store.es_aggregate_snapshot`).Scan(&snapshots).Error)
	if snapshots != 0 {
		t.Fatalf("interval 0 wrote %d snapshots", snapshots)
	}
}

// TestVehicleSnapshotRestoreThenAppend keeps the snapshot capability verified on the
// real store although runtime configuration disables it for vehicles.
func TestVehicleSnapshotRestoreThenAppend(t *testing.T) {
	db, store := newVehicleStore(t, 2)
	ctx := context.Background()

	v := &aggregates.VehicleInventoryAggregate{}
	must(t, v.SetID(vehicleID))
	must(t, v.Stock(aggregates.StockVehicle{
		DealershipID: "d-001", VIN: "1HGCM82633A004352", Make: "Toyota", Model: "Corolla",
		ModelYear: 2023, StockedOn: businessDate.AddDays(-120),
	}, businessDate))
	must(t, v.ProposeAction(aggregates.ProposeAction{ActionID: "a1", ActionType: domain.ActionPriceReduction, Note: "5%"}, businessDate))
	must(t, store.Save(ctx, v)) // version 2: snapshot
	must(t, v.ReviseAction(aggregates.ReviseAction{ActionID: "a1", ActionType: domain.ActionTransfer, Note: "site B"}))
	must(t, store.Save(ctx, v)) // version 3

	// Privileged test connection: remove the events the snapshot covers so the load can
	// only succeed through the snapshot.
	must(t, db.Exec(`DELETE FROM event_store.es_event WHERE aggregate_id = ? AND version <= 2`, vehicleID).Error)

	restored := loadVehicle(t, store)
	assertSameVehicle(t, restored, v)

	must(t, restored.Sell(aggregates.SellVehicle{SoldOn: businessDate}, businessDate))
	must(t, store.Save(ctx, restored)) // version 4: new snapshot
	if final := loadVehicle(t, store); final.Status() != domain.StatusSold || final.Version() != 4 {
		t.Fatalf("status=%s version=%d", final.Status(), final.Version())
	}
	var versions []int
	must(t, db.Raw(`SELECT version FROM event_store.es_aggregate_snapshot WHERE aggregate_id = ? ORDER BY version`, vehicleID).Scan(&versions).Error)
	if !reflect.DeepEqual(versions, []int{2, 4}) {
		t.Fatalf("snapshot versions %v", versions)
	}
}
