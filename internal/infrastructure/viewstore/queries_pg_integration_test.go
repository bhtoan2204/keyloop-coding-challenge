//go:build integration

package viewstore_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/internal/testsupport/pgtest"
	"keyloop-coding-challenge/package/eventsourcing"
)

var ctx = context.Background()

// London business date 2026-10-29, Sydney 2026-10-30.
var now = time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
var london = domain.NewDate(2026, 10, 29)

type clock struct{ t *time.Time }

func (c clock) Now() time.Time { return *c.t }

type fixture struct {
	store *viewstore.Postgres
	proj  projection.Projection
	svc   *query.Service
	now   *time.Time
	ids   int64
	dsn   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, dsn := pgtest.NewDatabase(t)
	if err := viewstore.Migrate(db); err != nil {
		t.Fatal(err)
	}
	store := viewstore.NewPostgres(db)
	n := now
	return &fixture{store: store, proj: projection.NewProjection(store, &projectors.VehicleProjector{}),
		svc: query.NewService(store, clock{&n}), now: &n, dsn: dsn}
}

type vspec struct {
	id, dealer, make, model string
	stockedOn               domain.Date
	action                  bool
	soldOn                  domain.Date
}

func (f *fixture) add(t *testing.T, v vspec) {
	t.Helper()
	version := 0
	apply := func(data interface{}) {
		version++
		f.ids++
		ev := eventsourcing.Event{ID: f.ids, AggregateID: v.id, AggregateType: "VehicleInventoryAggregate", Version: version,
			SchemaVersion: 1, Data: data, CreatedAt: now.Unix(), Metadata: map[string]interface{}{"actor_id": "m"}}
		if _, err := f.proj.Apply(ctx, ev); err != nil {
			t.Fatalf("seed %s: %v", v.id, err)
		}
	}
	if v.dealer == "" {
		v.dealer = "d-001"
	}
	if v.make == "" {
		v.make, v.model = "Toyota", "Corolla"
	}
	apply(&events.VehicleStockedEvent{DealershipID: v.dealer, VIN: "1HGCM82633A004352", Make: v.make, Model: v.model, ModelYear: 2023, StockedOn: v.stockedOn})
	closed := ""
	if v.action {
		apply(&events.VehicleActionProposedEvent{ActionID: "act-" + v.id, ActionType: domain.ActionHold})
		closed = "act-" + v.id
	}
	if !v.soldOn.IsZero() {
		apply(&events.VehicleSoldEvent{SoldOn: v.soldOn, ClosedActionID: closed})
	}
}

func ids(vs []query.Vehicle) []string {
	out := []string{}
	for _, v := range vs {
		out = append(out, v.VehicleID)
	}
	return out
}

func (f *fixture) list(t *testing.T, q query.ListInventory) *query.InventoryPage {
	t.Helper()
	if q.DealershipID == "" {
		q.DealershipID = "d-001"
	}
	page, err := f.svc.ListInventory(ctx, q)
	if err != nil {
		t.Fatalf("list %+v: %v", q, err)
	}
	return page
}

func intp(i int) *int { return &i }

func TestAgingBoundariesAndSoldExclusion(t *testing.T) {
	f := newFixture(t)
	f.add(t, vspec{id: "d89", stockedOn: london.AddDays(-89)})
	f.add(t, vspec{id: "d90", stockedOn: london.AddDays(-90)})
	f.add(t, vspec{id: "d91", stockedOn: london.AddDays(-91)})
	f.add(t, vspec{id: "sold-old", stockedOn: london.AddDays(-300), soldOn: london.AddDays(-10)})

	page := f.list(t, query.ListInventory{})
	if got := ids(page.Vehicles); !reflect.DeepEqual(got, []string{"d91", "d90", "d89"}) || page.AsOf != london {
		t.Fatalf("default list %v as_of %s", got, page.AsOf)
	}
	for _, v := range page.Vehicles {
		wantAge := map[string]int{"d89": 89, "d90": 90, "d91": 91}[v.VehicleID]
		if v.AgeDays(page.AsOf) != wantAge || v.IsAging(page.AsOf) != (wantAge == 91) {
			t.Fatalf("%s: age %d aging %v", v.VehicleID, v.AgeDays(page.AsOf), v.IsAging(page.AsOf))
		}
	}
	if got := ids(f.list(t, query.ListInventory{AgingOnly: true}).Vehicles); !reflect.DeepEqual(got, []string{"d91"}) {
		t.Fatalf("aging only %v", got)
	}
	if got := ids(f.list(t, query.ListInventory{AgingOnly: true, Status: "ALL"}).Vehicles); !reflect.DeepEqual(got, []string{"d91"}) {
		t.Fatalf("aging with ALL must still exclude sold: %v", got)
	}
	if got := f.list(t, query.ListInventory{AgingOnly: true, Status: "SOLD"}).Vehicles; len(got) != 0 {
		t.Fatalf("aging + SOLD must be empty: %v", ids(got))
	}
	sold := f.list(t, query.ListInventory{Status: "SOLD"}).Vehicles
	if len(sold) != 1 || sold[0].AgeDays(london) != 290 || sold[0].IsAging(london) {
		t.Fatalf("sold vehicle: %+v", sold)
	}
}

func TestDealershipTimeZoneAndDST(t *testing.T) {
	f := newFixture(t)
	f.add(t, vspec{id: "lon", dealer: "d-001", stockedOn: domain.NewDate(2026, 7, 31)})
	f.add(t, vspec{id: "syd", dealer: "d-002", stockedOn: domain.NewDate(2026, 7, 31)})

	// 14:00 UTC: London 2026-10-29 (90 days, not aging), Sydney 2026-10-30 (91, aging).
	if got := f.list(t, query.ListInventory{DealershipID: "d-001", AgingOnly: true}).Vehicles; len(got) != 0 {
		t.Fatalf("London aging: %v", ids(got))
	}
	if got := ids(f.list(t, query.ListInventory{DealershipID: "d-002", AgingOnly: true}).Vehicles); !reflect.DeepEqual(got, []string{"syd"}) {
		t.Fatalf("Sydney aging: %v", got)
	}
	// DST: London leaves BST on 2026-10-25 at 01:00 UTC.
	for instant, want := range map[time.Time]domain.Date{
		time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC): domain.NewDate(2026, 10, 25), // 00:30 BST
		time.Date(2026, 10, 25, 23, 30, 0, 0, time.UTC): domain.NewDate(2026, 10, 25), // 23:30 GMT
	} {
		*f.now = instant
		res, err := f.svc.GetInventorySummary(ctx, "d-001", nil)
		if err != nil || res.AsOf != want {
			t.Fatalf("at %s: as_of %s, want %s (%v)", instant, res.AsOf, want, err)
		}
	}
}

func TestMakeModelAgeAndStatusFilters(t *testing.T) {
	f := newFixture(t)
	f.add(t, vspec{id: "t-corolla", make: "Toyota", model: "Corolla", stockedOn: london.AddDays(-10)})
	f.add(t, vspec{id: "t-rav4", make: "Toyota", model: "RAV4", stockedOn: london.AddDays(-50)})
	f.add(t, vspec{id: "m-cx5", make: "Mazda", model: "CX-5", stockedOn: london.AddDays(-100)})
	f.add(t, vspec{id: "t-sold", make: "Toyota", model: "Corolla", stockedOn: london.AddDays(-200), soldOn: london.AddDays(-150)}) // sold at 50 days

	cases := []struct {
		name string
		q    query.ListInventory
		want []string
	}{
		{"make, case-insensitive", query.ListInventory{Make: "toyota"}, []string{"t-rav4", "t-corolla"}},
		{"make + model", query.ListInventory{Make: "TOYOTA", Model: "corolla"}, []string{"t-corolla"}},
		{"min age inclusive", query.ListInventory{MinAgeDays: intp(50)}, []string{"m-cx5", "t-rav4"}},
		{"max age inclusive", query.ListInventory{MaxAgeDays: intp(50)}, []string{"t-rav4", "t-corolla"}},
		{"age range", query.ListInventory{MinAgeDays: intp(11), MaxAgeDays: intp(99)}, []string{"t-rav4"}},
		{"status SOLD", query.ListInventory{Status: "SOLD"}, []string{"t-sold"}},
		{"status ALL", query.ListInventory{Status: "ALL"}, []string{"t-sold", "m-cx5", "t-rav4", "t-corolla"}},
		{"sold age is age at sale", query.ListInventory{Status: "ALL", MinAgeDays: intp(50), MaxAgeDays: intp(50)}, []string{"t-sold", "t-rav4"}},
		{"youngest first", query.ListInventory{Sort: query.SortAgeAsc}, []string{"t-corolla", "t-rav4", "m-cx5"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ids(f.list(t, tc.q).Vehicles); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	// Invalid filters are validation errors.
	for _, bad := range []query.ListInventory{
		{DealershipID: "d-001", Limit: 101}, {DealershipID: "d-001", Limit: -1}, {DealershipID: "d-001", Status: "LOST"},
		{DealershipID: "d-001", Sort: "price"}, {DealershipID: "d-001", MinAgeDays: intp(10), MaxAgeDays: intp(5)},
		{DealershipID: "d-001", MinAgeDays: intp(-1)},
	} {
		if _, err := f.svc.ListInventory(ctx, bad); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if _, err := f.svc.ListInventory(ctx, query.ListInventory{DealershipID: "d-404"}); !errors.Is(err, query.ErrDealershipNotFound) {
		t.Errorf("unknown dealership: %v", err)
	}
}

// TestKeysetPaginationOverManyRowsWithDuplicateSortKeys: 260 vehicles share only 13
// stock dates, so the vehicle-ID tie-breaker decides most of the order.
func TestKeysetPaginationOverManyRowsWithDuplicateSortKeys(t *testing.T) {
	f := newFixture(t)
	type key struct {
		date domain.Date
		id   string
	}
	var all []key
	for i := 0; i < 260; i++ {
		k := key{london.AddDays(-(i % 13) * 9), fmt.Sprintf("v%03d", (i*37)%260)}
		f.add(t, vspec{id: k.id, stockedOn: k.date})
		all = append(all, k)
	}
	// Second pass without index scans: rows then come from a sequential scan and an
	// explicit sort, so only the ORDER BY (with its vehicle_id tie-breaker) decides the
	// order of equal stock dates, not the index's physical order.
	noIndex := query.NewService(viewstore.NewPostgres(pgtest.Open(t, f.dsn+"&enable_indexscan=off&enable_bitmapscan=off")), clock{f.now})
	for pass, svc := range []*query.Service{f.svc, noIndex} {
		for _, desc := range []bool{false, true} {
			want := append([]key(nil), all...)
			sort.Slice(want, func(i, j int) bool {
				a, b := want[i], want[j]
				if a.date != b.date {
					return a.date.Before(b.date) != desc
				}
				return (a.id < b.id) != desc
			})
			var wantIDs, got []string
			for _, k := range want {
				wantIDs = append(wantIDs, k.id)
			}
			sortKey := query.SortAgeDesc
			if desc {
				sortKey = query.SortAgeAsc
			}
			cursor, pages := "", 0
			for {
				page, err := svc.ListInventory(ctx, query.ListInventory{DealershipID: "d-001", Sort: sortKey, Limit: 7, Cursor: cursor})
				if err != nil {
					t.Fatal(err)
				}
				pages++
				got = append(got, ids(page.Vehicles)...)
				if !page.HasMore {
					if page.NextCursor != "" {
						t.Fatal("next_cursor without has_more")
					}
					break
				}
				cursor = page.NextCursor
			}
			if !reflect.DeepEqual(got, wantIDs) {
				t.Fatalf("pass %d %s: paged order differs from full order (pages=%d)", pass, sortKey, pages)
			}
			if pages != 38 { // ceil(260/7)
				t.Fatalf("pass %d %s: %d pages", pass, sortKey, pages)
			}
		}
	}
	if def := f.list(t, query.ListInventory{}); def.Limit != 20 || len(def.Vehicles) != 20 || !def.HasMore {
		t.Fatalf("default limit: %d rows, limit %d", len(def.Vehicles), def.Limit)
	}
	if max := f.list(t, query.ListInventory{Limit: 100}); len(max.Vehicles) != 100 {
		t.Fatalf("max limit: %d", len(max.Vehicles))
	}
}

func TestCursorValidationAndPinnedBusinessDate(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 5; i++ {
		f.add(t, vspec{id: fmt.Sprintf("v%d", i), stockedOn: london.AddDays(-80 - i)})
	}
	first := f.list(t, query.ListInventory{Limit: 2})

	bad := []query.ListInventory{
		{Limit: 2, Cursor: "not-base64!"},
		{Limit: 2, Cursor: "eyJ2IjoxfQ"}, // {"v":1} without position
		{Limit: 2, Cursor: first.NextCursor, Make: "Mazda"},
		{Limit: 2, Cursor: first.NextCursor, Sort: query.SortAgeAsc},
		{Limit: 2, Cursor: first.NextCursor, AgingOnly: true},
		{Limit: 2, Cursor: first.NextCursor, AsOf: &[]domain.Date{london.AddDays(1)}[0]},
	}
	for _, q := range bad {
		q.DealershipID = "d-001"
		if _, err := f.svc.ListInventory(ctx, q); !errors.Is(err, query.ErrInvalidCursor) {
			t.Errorf("%+v: %v", q, err)
		}
	}
	// A different limit is allowed with the same cursor.
	f.list(t, query.ListInventory{Limit: 3, Cursor: first.NextCursor})

	// The clock passes dealership-local midnight between pages: the cursor keeps the
	// first page's business date, so ages and aging stay consistent across pages.
	*f.now = now.Add(24 * time.Hour)
	second := f.list(t, query.ListInventory{Limit: 2, Cursor: first.NextCursor})
	if second.AsOf != first.AsOf {
		t.Fatalf("as_of moved from %s to %s", first.AsOf, second.AsOf)
	}
	if fresh := f.list(t, query.ListInventory{Limit: 2}); fresh.AsOf != london.AddDays(1) {
		t.Fatalf("new listing as_of %s", fresh.AsOf)
	}
}

func TestSummaryMatchesListAndOwnership(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 40; i++ {
		v := vspec{id: fmt.Sprintf("v%02d", i), stockedOn: london.AddDays(-(i * 7)), action: i%4 == 0 && i*7 > 90}
		if i%9 == 0 {
			v.soldOn = london
		}
		f.add(t, v)
	}
	f.add(t, vspec{id: "other", dealer: "d-002", stockedOn: london.AddDays(-200)})

	count := func(q query.ListInventory) (n, withoutAction int) {
		q.Limit = 100
		for _, v := range f.list(t, q).Vehicles {
			n++
			if v.Action == nil {
				withoutAction++
			}
		}
		return
	}
	inStock, _ := count(query.ListInventory{})
	aging, agingNoAction := count(query.ListInventory{AgingOnly: true})
	sum, err := f.svc.GetInventorySummary(ctx, "d-001", nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.InStock != inStock || sum.Aging != aging || sum.AgingWithoutAction != agingNoAction || aging == 0 || agingNoAction == aging {
		t.Fatalf("summary %+v vs list in_stock=%d aging=%d without_action=%d", sum.Summary, inStock, aging, agingNoAction)
	}

	if _, err := f.svc.GetVehicle(ctx, "d-002", "v01", nil); !errors.Is(err, query.ErrVehicleNotFound) {
		t.Fatalf("cross-dealership vehicle: %v", err)
	}
	if _, err := f.svc.GetActionHistory(ctx, "d-002", "v04", 0, ""); !errors.Is(err, query.ErrVehicleNotFound) {
		t.Fatalf("cross-dealership history: %v", err)
	}
	h, err := f.svc.GetActionHistory(ctx, "d-001", "v28", 0, "")
	if err != nil || len(h.Entries) != 1 || h.Entries[0].Change != query.ChangeProposed {
		t.Fatalf("history: %+v %v", h, err)
	}
	if res, err := f.svc.GetVehicle(ctx, "d-001", "v28", nil); err != nil || res.Vehicle.Action == nil || res.Vehicle.Action.UpdatedBy != "m" {
		t.Fatalf("vehicle: %+v %v", res, err)
	}
}
