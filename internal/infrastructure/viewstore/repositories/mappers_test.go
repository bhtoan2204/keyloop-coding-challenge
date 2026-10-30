package repositories

import (
	"reflect"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/application/projection"
	"keyloop-coding-challenge/internal/domain"
)

func TestProjectedVehicleRoundTripsThroughTheRow(t *testing.T) {
	sydney, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Fatal(err)
	}
	withAction := projection.ProjectedVehicle{
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
		ActionProposedAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		ActionUpdatedAt:  time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC),
		ActionUpdatedBy:  "manager-1",
		Version:          2,
		LastEventID:      42,
		UpdatedAt:        time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC),
	}
	sold := projection.ProjectedVehicle{
		VehicleID:    "v2",
		DealershipID: "d-001",
		StockedOn:    domain.NewDate(2025, 12, 31),
		Status:       domain.StatusSold,
		SoldOn:       domain.NewDate(2026, 1, 1),
		Version:      3,
		LastEventID:  7,
		UpdatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	for _, v := range []projection.ProjectedVehicle{withAction, sold} {
		if got := toProjectedVehicle(toInventoryVehicle(v)); !reflect.DeepEqual(got, v) {
			t.Fatalf("round trip\n got %+v\nwant %+v", got, v)
		}
	}

	row := toInventoryVehicle(sold)
	if row.SoldOn.Date.String() != "2026-01-01" || !row.SoldOn.Valid || row.ActionID.Valid || row.ActionProposedAt.Valid {
		t.Fatalf("sold row nulls: %+v", row)
	}
	if row := toInventoryVehicle(withAction); row.SoldOn.Valid || !row.ActionID.Valid || row.StockedOn.String() != "2024-02-29" {
		t.Fatalf("in-stock row: %+v", row)
	}

	// Timestamps read back in another zone are returned in UTC; the query model only has
	// an action when the action columns are set.
	row = toInventoryVehicle(withAction)
	row.UpdatedAt = row.UpdatedAt.In(sydney)
	row.ActionProposedAt.Time = row.ActionProposedAt.Time.In(sydney)
	q := toQueryVehicle(row)
	if q.UpdatedAt.Location() != time.UTC || q.Action == nil || q.Action.ProposedAt.Location() != time.UTC || !q.Action.ProposedAt.Equal(withAction.ActionProposedAt) {
		t.Fatalf("query vehicle: %+v", q)
	}
	if q := toQueryVehicle(toInventoryVehicle(sold)); q.Action != nil || q.SoldOn != sold.SoldOn {
		t.Fatalf("sold query vehicle: %+v", q)
	}
}

func TestActionHistoryEntryMapping(t *testing.T) {
	e := projection.ActionHistoryEntry{
		VehicleID:     "v1",
		Version:       3,
		EventID:       99,
		ActionID:      "a1",
		Change:        "REVISED",
		ActionType:    domain.ActionAuction,
		Note:          "lot 7",
		ActorID:       "manager-1",
		CorrelationID: "req-1",
		OccurredAt:    time.Date(2026, 10, 3, 8, 0, 0, 0, time.FixedZone("x", 3600)),
	}
	h := toQueryHistoryEntry(toActionHistoryRow(e))
	if h.Version != 3 || h.EventID != 99 || h.ActionType != domain.ActionAuction || h.Change != "REVISED" ||
		h.OccurredAt.Location() != time.UTC || !h.OccurredAt.Equal(e.OccurredAt) {
		t.Fatalf("history entry: %+v", h)
	}
}
