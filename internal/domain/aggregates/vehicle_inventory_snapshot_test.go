package aggregates

import (
	"context"
	"strings"
	"testing"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestSnapshotJSONRoundTrip restores every business field. businessState compares the
// whole struct, so a state field added later without a snapshot field fails here.
func TestSnapshotJSONRoundTrip(t *testing.T) {
	scenarios := map[string]func(*testing.T) *VehicleInventoryAggregate{
		"stocked":     func(t *testing.T) *VehicleInventoryAggregate { return stockedFor(t, 30) },
		"with action": withAction,
		"sold":        sold,
	}
	serializer := eventsourcing.NewSerializer()
	for name, build := range scenarios {
		t.Run(name, func(t *testing.T) {
			original := build(t)
			data, err := serializer.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) == "{}" {
				t.Fatal("snapshot is empty")
			}
			restored := &VehicleInventoryAggregate{}
			if err := serializer.Unmarshal(data, restored); err != nil {
				t.Fatal(err)
			}
			assertSameState(t, restored, original)
		})
	}
}

func TestSnapshotStoresNoDerivedValues(t *testing.T) {
	data, err := withAction(t).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, derived := range []string{"aging", "days", "age"} {
		if strings.Contains(strings.ToLower(string(data)), derived) {
			t.Fatalf("snapshot contains derived value %q: %s", derived, data)
		}
	}
}

// TestSnapshotThroughAggregateStore uses the existing AggregateStore (SQLite) to
// check that a vehicle restored from a snapshot equals the vehicle that was saved,
// even when the events covered by the snapshot are no longer readable.
func TestSnapshotThroughAggregateStore(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:vehicle_snapshot?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&VehicleInventoryAggregate{}); err != nil {
		t.Fatal(err)
	}
	store := eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(db, serializer), 2)
	ctx := context.Background()

	a := newVehicle(t)
	mustDo(t, a.Stock(validStock(bizDate.AddDays(-120)), bizDate))
	mustDo(t, store.Save(ctx, a))
	mustDo(t, a.ProposeAction(ProposeAction{ActionID: testActionID, ActionType: domain.ActionPriceReduction, Note: "5%"}, bizDate))
	mustDo(t, store.Save(ctx, a)) // version 2: snapshot
	mustDo(t, a.ReviseAction(ReviseAction{ActionID: testActionID, ActionType: domain.ActionAuction, Note: "auction"}))
	mustDo(t, store.Save(ctx, a)) // version 3: replayed on top of the snapshot

	var snapshots int64
	if err := db.Raw(`SELECT COUNT(*) FROM es_aggregate_snapshot WHERE aggregate_id = ? AND version = 2`, testVehicleID).Scan(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	if snapshots != 1 {
		t.Fatalf("snapshots at version 2: %d", snapshots)
	}
	// Remove the events the snapshot covers: the load must come from the snapshot.
	if err := db.Exec(`DELETE FROM es_event WHERE aggregate_id = ? AND version <= 2`, testVehicleID).Error; err != nil {
		t.Fatal(err)
	}

	loaded := &VehicleInventoryAggregate{}
	if err := store.Get(ctx, testVehicleID, loaded); err != nil {
		t.Fatal(err)
	}
	assertSameState(t, loaded, a)
	if loaded.Version() != 3 || loaded.IsUnsaved() {
		t.Fatalf("loaded version=%d unsaved=%v", loaded.Version(), loaded.IsUnsaved())
	}

	// The restored aggregate keeps enforcing invariants and continues the stream.
	assertRejected(t, loaded, func() error {
		return loaded.ProposeAction(ProposeAction{ActionID: "second", ActionType: domain.ActionHold}, bizDate)
	}, domain.ErrActionAlreadyActive)
	mustDo(t, loaded.Sell(SellVehicle{SoldOn: bizDate}, bizDate))
	mustDo(t, store.Save(ctx, loaded))
	if loaded.Version() != 4 || loaded.Status() != domain.StatusSold {
		t.Fatalf("after sale version=%d status=%s", loaded.Version(), loaded.Status())
	}
}
