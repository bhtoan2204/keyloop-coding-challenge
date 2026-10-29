package query_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
)

// fakeReadModel serves vehicles from memory in (stocked_on, vehicle_id) order and
// records the last filter it received.
type fakeReadModel struct {
	vehicles []query.Vehicle
	last     query.VehicleFilter
}

func (f *fakeReadModel) DealershipLocation(_ context.Context, id string) (*time.Location, error) {
	switch id {
	case "d-001":
		return time.LoadLocation("Europe/London")
	case "d-002":
		return time.LoadLocation("Australia/Sydney")
	}
	return nil, fmt.Errorf("%w: %s", query.ErrDealershipNotFound, id)
}

func (f *fakeReadModel) ListVehicles(_ context.Context, flt query.VehicleFilter) ([]query.Vehicle, error) {
	f.last = flt
	var out []query.Vehicle
	for _, v := range f.vehicles {
		if flt.After != nil && !(v.StockedOn.After(flt.After.StockedOn) || v.StockedOn == flt.After.StockedOn && v.VehicleID > flt.After.VehicleID) {
			continue
		}
		out = append(out, v)
		if len(out) == flt.Limit {
			break
		}
	}
	return out, nil
}

func (f *fakeReadModel) GetVehicle(_ context.Context, d, id string) (*query.Vehicle, error) {
	for _, v := range f.vehicles {
		if v.VehicleID == id && v.DealershipID == d {
			return &v, nil
		}
	}
	return nil, query.ErrVehicleNotFound
}

func (f *fakeReadModel) ListHistory(context.Context, string, int, int) ([]query.HistoryEntry, error) {
	return nil, nil
}

func (f *fakeReadModel) Summary(context.Context, string, domain.Date) (query.Summary, error) {
	return query.Summary{}, nil
}

type fixedClock struct{ t *time.Time }

func (c fixedClock) Now() time.Time { return *c.t }

func newService(n int) (*query.Service, *fakeReadModel, *time.Time) {
	rm := &fakeReadModel{}
	for i := 0; i < n; i++ {
		rm.vehicles = append(rm.vehicles, query.Vehicle{VehicleID: fmt.Sprintf("v%02d", i), DealershipID: "d-001",
			StockedOn: domain.NewDate(2026, 7, 1).AddDays(i / 2), Status: domain.StatusInStock})
	}
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
	return query.NewService(rm, fixedClock{&now}), rm, &now
}

func TestReadSideAgingRule(t *testing.T) {
	bd := domain.NewDate(2026, 10, 29)
	for days, aging := range map[int]bool{89: false, 90: false, 91: true} {
		v := query.Vehicle{Status: domain.StatusInStock, StockedOn: bd.AddDays(-days)}
		if v.AgeDays(bd) != days || v.IsAging(bd) != aging {
			t.Errorf("%d days: age %d aging %v", days, v.AgeDays(bd), v.IsAging(bd))
		}
	}
	sold := query.Vehicle{Status: domain.StatusSold, StockedOn: bd.AddDays(-300), SoldOn: bd.AddDays(-100)}
	if sold.AgeDays(bd) != 200 || sold.IsAging(bd) {
		t.Errorf("sold: age %d aging %v", sold.AgeDays(bd), sold.IsAging(bd))
	}
}

func TestListBuildsFilterAndBusinessDate(t *testing.T) {
	svc, rm, _ := newService(3)
	min := 10
	page, err := svc.ListInventory(context.Background(), query.ListInventory{DealershipID: "d-002", Make: " Toyota ", MinAgeDays: &min, AgingOnly: true, Sort: "age_asc"})
	if err != nil {
		t.Fatal(err)
	}
	f := rm.last
	if f.BusinessDate != domain.NewDate(2026, 10, 30) || page.AsOf != f.BusinessDate { // Sydney is a day ahead
		t.Fatalf("business date %s", f.BusinessDate)
	}
	if f.Make != "Toyota" || !f.AgingOnly || !f.Descending || *f.MinAgeDays != 10 || len(f.Statuses) != 1 || f.Limit != query.DefaultLimit+1 {
		t.Fatalf("filter %+v", f)
	}
	if _, err := svc.ListInventory(context.Background(), query.ListInventory{DealershipID: "d-001", Status: "ALL"}); err != nil || len(rm.last.Statuses) != 0 {
		t.Fatalf("ALL: %v %+v", err, rm.last.Statuses)
	}
}

func TestCursorRules(t *testing.T) {
	svc, _, now := newService(9)
	ctx := context.Background()
	first, err := svc.ListInventory(ctx, query.ListInventory{DealershipID: "d-001", Limit: 2})
	if err != nil || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	// A changed limit is fine; everything else must match the cursor.
	if _, err := svc.ListInventory(ctx, query.ListInventory{DealershipID: "d-001", Limit: 3, Cursor: first.NextCursor}); err != nil {
		t.Fatalf("limit change: %v", err)
	}
	other := domain.NewDate(2026, 10, 1)
	for name, q := range map[string]query.ListInventory{
		"other make":       {Make: "Mazda"},
		"other sort":       {Sort: query.SortAgeAsc},
		"aging toggled":    {AgingOnly: true},
		"other status":     {Status: "ALL"},
		"other dealership": {DealershipID: "d-002"},
		"other as_of":      {AsOf: &other},
		"garbage":          {Cursor: "%%%"},
		"tampered":         {Cursor: first.NextCursor[:len(first.NextCursor)-2] + "zz"},
	} {
		if q.DealershipID == "" {
			q.DealershipID = "d-001"
		}
		if q.Cursor == "" {
			q.Cursor = first.NextCursor
		}
		if _, err := svc.ListInventory(ctx, q); !errors.Is(err, query.ErrInvalidCursor) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The cursor pins the business date across midnight.
	*now = now.Add(24 * time.Hour)
	second, err := svc.ListInventory(ctx, query.ListInventory{DealershipID: "d-001", Limit: 2, Cursor: first.NextCursor})
	if err != nil || second.AsOf != first.AsOf {
		t.Fatalf("pinned as_of: %v %v", second.AsOf, err)
	}
	// Walking all pages returns every vehicle exactly once.
	seen, cursor := map[string]bool{}, ""
	for {
		page, err := svc.ListInventory(ctx, query.ListInventory{DealershipID: "d-001", Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range page.Vehicles {
			if seen[v.VehicleID] {
				t.Fatalf("%s returned twice", v.VehicleID)
			}
			seen[v.VehicleID] = true
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 9 {
		t.Fatalf("saw %d of 9", len(seen))
	}
}

func TestValidationAndOwnership(t *testing.T) {
	svc, _, _ := newService(1)
	ctx := context.Background()
	for _, q := range []query.ListInventory{{Limit: 101}, {Limit: -1}, {Status: "LOST"}, {Sort: "price"}} {
		q.DealershipID = "d-001"
		if _, err := svc.ListInventory(ctx, q); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%+v: %v", q, err)
		}
	}
	if _, err := svc.GetVehicle(ctx, "d-002", "v00", nil); !errors.Is(err, query.ErrVehicleNotFound) {
		t.Errorf("cross-dealership: %v", err)
	}
	if _, err := svc.GetActionHistory(ctx, "d-001", "v00", 0, "not-a-cursor"); !errors.Is(err, query.ErrInvalidCursor) {
		t.Errorf("history cursor: %v", err)
	}
	if _, err := svc.GetInventorySummary(ctx, "d-404", nil); !errors.Is(err, query.ErrDealershipNotFound) {
		t.Errorf("unknown dealership: %v", err)
	}
}
