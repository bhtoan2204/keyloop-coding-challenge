//go:build integration

package viewstore_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/application/projection"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/internal/testsupport/pgtest"
)

// TestProjectionStoreWritesAreTransactionBound: the repositories behind the store write
// only inside its transaction and commit or roll back with it. Values survive the
// database round trip unchanged, also in a session time zone west of UTC.
func TestProjectionStoreWritesAreTransactionBound(t *testing.T) {
	ctx := context.Background()
	admin, dsn := pgtest.NewDatabase(t)
	if err := repos.Migrate(admin); err != nil {
		t.Fatal(err)
	}
	if err := viewstore.Migrate(admin); err != nil {
		t.Fatal(err)
	}
	store := viewstore.NewPostgres(pgtest.Open(t, dsn+"&timezone=America/Los_Angeles"))

	v := projection.ProjectedVehicle{
		VehicleID:        "v1",
		DealershipID:     "d-001",
		VIN:              "1HGCM82633A004352",
		Make:             "Toyota",
		Model:            "Corolla",
		ModelYear:        2023,
		StockedOn:        domain.NewDate(2024, 2, 29),
		Status:           domain.StatusInStock,
		ActionID:         "a1",
		ActionType:       domain.ActionHold,
		ActionNote:       "n",
		ActionProposedAt: time.Date(2026, 10, 1, 23, 30, 0, 0, time.UTC),
		ActionUpdatedAt:  time.Date(2026, 10, 1, 23, 30, 0, 0, time.UTC),
		ActionUpdatedBy:  "manager-1",
		Version:          2,
		LastEventID:      42,
		UpdatedAt:        time.Date(2026, 10, 1, 23, 30, 0, 0, time.UTC),
	}
	if err := store.SaveVehicle(ctx, v); !errors.Is(err, viewstore.ErrNotInTransaction) {
		t.Fatalf("write outside a transaction: %v", err)
	}

	err := store.WithTransaction(ctx, func(tx projection.Store) error {
		if err := tx.SaveVehicle(ctx, v); err != nil {
			return err
		}
		if err := tx.AppendHistory(ctx, projection.ActionHistoryEntry{
			VehicleID:  "v1",
			Version:    2,
			EventID:    42,
			ActionID:   "a1",
			Change:     "PROPOSED",
			ActionType: domain.ActionHold,
			OccurredAt: v.ActionProposedAt,
		}); err != nil {
			return err
		}
		return tx.SetAppliedVersion(ctx, "v1", 2, 42)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Vehicle(ctx, "v1")
	if err != nil || got == nil || !reflect.DeepEqual(*got, v) {
		t.Fatalf("round trip: %+v %v\nwant %+v", got, err, v)
	}
	var stockedOn string
	admin.Raw(`SELECT stocked_on::text FROM read_model.inventory_vehicle WHERE vehicle_id = 'v1'`).Scan(&stockedOn)
	if stockedOn != "2024-02-29" {
		t.Fatalf("stored stocked_on %s", stockedOn)
	}

	// Sold, with the action cleared: NULL columns, and a failure later in the same
	// transaction undoes the vehicle, history and version writes.
	sold := v
	sold.Status, sold.SoldOn, sold.Version = domain.StatusSold, domain.NewDate(2026, 10, 29), 3
	sold.ActionID, sold.ActionType, sold.ActionNote, sold.ActionUpdatedBy = "", "", "", ""
	sold.ActionProposedAt, sold.ActionUpdatedAt = time.Time{}, time.Time{}
	boom := errors.New("later projector failed")
	err = store.WithTransaction(ctx, func(tx projection.Store) error {
		if err := tx.SaveVehicle(ctx, sold); err != nil {
			return err
		}
		if err := tx.SetAppliedVersion(ctx, "v1", 3, 43); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if got, _ := store.Vehicle(ctx, "v1"); !reflect.DeepEqual(*got, v) {
		t.Fatalf("rolled-back write is visible: %+v", got)
	}
	var applied int
	admin.Raw(`SELECT version FROM read_model.projection_aggregate WHERE aggregate_id = 'v1'`).Scan(&applied)
	if applied != 2 {
		t.Fatalf("applied version after rollback: %d", applied)
	}

	err = store.WithTransaction(ctx, func(tx projection.Store) error {
		return tx.SaveVehicle(ctx, sold)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Vehicle(ctx, "v1"); !reflect.DeepEqual(*got, sold) {
		t.Fatalf("sold round trip: %+v\nwant %+v", got, sold)
	}
	var nulls bool
	admin.Raw(`SELECT action_id IS NULL AND action_proposed_at IS NULL AND sold_on = DATE '2026-10-29'
		FROM read_model.inventory_vehicle WHERE vehicle_id = 'v1'`).Scan(&nulls)
	if !nulls {
		t.Fatal("cleared action is not stored as NULL")
	}
}
