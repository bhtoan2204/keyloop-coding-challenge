package presentation_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/presentation"
)

// stubQueries records the last list request and returns fixed results or an error.
type stubQueries struct {
	err  error
	last *query.ListInventory
}

var asOf = domain.NewDate(2026, 10, 29)

var sample = query.Vehicle{
	VehicleID: "v1", DealershipID: "d-001", VIN: "1HGCM82633A004352", Make: "Toyota", Model: "Corolla", ModelYear: 2023,
	StockedOn: domain.NewDate(2026, 7, 30), Status: domain.StatusInStock, Version: 2,
	Action: &query.Action{ID: "a1", Type: domain.ActionHold, ProposedAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC), UpdatedBy: "manager-42"},
}

func (s *stubQueries) ListInventory(_ context.Context, q query.ListInventory) (*query.InventoryPage, error) {
	s.last = &q
	if s.err != nil {
		return nil, s.err
	}
	return &query.InventoryPage{AsOf: asOf, Vehicles: []query.Vehicle{sample}, Limit: 20, HasMore: true, NextCursor: "abc"}, nil
}
func (s *stubQueries) GetVehicle(context.Context, string, string, *domain.Date) (*query.VehicleResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &query.VehicleResult{Vehicle: sample, AsOf: asOf}, nil
}
func (s *stubQueries) GetActionHistory(context.Context, string, string, int, string) (*query.HistoryPage, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &query.HistoryPage{Limit: 20, Entries: []query.HistoryEntry{{Version: 2, EventID: 7, ActionID: "a1",
		Change: query.ChangeProposed, ActionType: domain.ActionHold, ActorID: "manager-42", OccurredAt: sample.Action.ProposedAt}}}, nil
}
func (s *stubQueries) GetInventorySummary(context.Context, string, *domain.Date) (*query.SummaryResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &query.SummaryResult{AsOf: asOf, Summary: query.Summary{InStock: 3, Aging: 1}}, nil
}

func newQueryServer(t *testing.T, q *stubQueries) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(presentation.NewHandler(stubService{}, q, nil, discard))
	t.Cleanup(srv.Close)
	return srv
}

func TestQueryParameterParsing(t *testing.T) {
	q := &stubQueries{}
	srv := newQueryServer(t, q)
	r := do(t, srv, call{method: "GET", path: vehicles + "?make=Toyota&model=Corolla&min_age_days=10&max_age_days=200&aging=true&status=ALL&sort=age_asc&limit=5&cursor=xyz&as_of=2026-10-20"})
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.rawBody)
	}
	asOf := domain.NewDate(2026, 10, 20)
	min, max := 10, 200
	want := query.ListInventory{DealershipID: "d-001", Make: "Toyota", Model: "Corolla", MinAgeDays: &min, MaxAgeDays: &max,
		AgingOnly: true, Status: "ALL", Sort: "age_asc", Limit: 5, Cursor: "xyz", AsOf: &asOf}
	if !reflect.DeepEqual(*q.last, want) {
		t.Fatalf("parsed %+v", *q.last)
	}

	cases := []struct {
		path, code string
	}{
		{vehicles + "?agng=true", "INVALID_QUERY"},
		{vehicles + "?make=a&make=b", "INVALID_QUERY"},
		{vehicles + "?limit=ten", "VALIDATION_ERROR"},
		{vehicles + "?aging=yes", "VALIDATION_ERROR"},
		{vehicles + "?as_of=29-10-2026", "VALIDATION_ERROR"},
		{vehicles + "/v1?status=SOLD", "INVALID_QUERY"},
		{"/v1/dealerships/d-001/inventory/summary?limit=3", "INVALID_QUERY"},
	}
	for _, tc := range cases {
		if got := do(t, srv, call{method: "GET", path: tc.path}); got.status != 400 || got.code() != tc.code {
			t.Errorf("%s: %d %s", tc.path, got.status, got.rawBody)
		}
	}
}

func TestQueryResponses(t *testing.T) {
	srv := newQueryServer(t, &stubQueries{})
	list := do(t, srv, call{method: "GET", path: vehicles})
	item := list.body["data"].([]interface{})[0].(map[string]interface{})
	page := list.body["page"].(map[string]interface{})
	if list.body["as_of"] != "2026-10-29" || list.body["aging_threshold_days"] != 90.0 || item["age_days"] != 91.0 ||
		item["is_aging"] != true || page["next_cursor"] != "abc" || page["has_more"] != true {
		t.Fatalf("list: %s", list.rawBody)
	}
	action := item["current_action"].(map[string]interface{})
	if action["updated_by"] != "manager-42" || action["proposed_at"] != "2026-10-01T09:00:00Z" {
		t.Fatalf("action: %v", action)
	}
	detail := do(t, srv, call{method: "GET", path: vehicles + "/v1"})
	if detail.status != 200 || detail.header.Get("ETag") != `"2"` || detail.body["as_of"] != "2026-10-29" {
		t.Fatalf("detail: %d %v %s", detail.status, detail.header, detail.rawBody)
	}
	history := do(t, srv, call{method: "GET", path: vehicles + "/v1/actions"})
	entry := history.body["data"].([]interface{})[0].(map[string]interface{})
	if entry["change"] != "PROPOSED" || entry["event_id"] != 7.0 || history.body["page"].(map[string]interface{})["next_cursor"] != nil {
		t.Fatalf("history: %s", history.rawBody)
	}
	sum := do(t, srv, call{method: "GET", path: "/v1/dealerships/d-001/inventory/summary"})
	if sum.body["in_stock"] != 3.0 || sum.body["aging"] != 1.0 || sum.body["oldest_stocked_on"] != nil {
		t.Fatalf("summary: %s", sum.rawBody)
	}
}

func TestQueryErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("x: %w", query.ErrDealershipNotFound), 404, "DEALERSHIP_NOT_FOUND"},
		{fmt.Errorf("x: %w", query.ErrVehicleNotFound), 404, "VEHICLE_NOT_FOUND"},
		{fmt.Errorf("x: %w", query.ErrInvalidCursor), 400, "INVALID_CURSOR"},
		{domain.Invalid("limit", "must be between 1 and 100"), 400, "VALIDATION_ERROR"},
		{fmt.Errorf("pq: relation inventory_vehicle: connection refused"), 500, "INTERNAL"},
	}
	for _, tc := range cases {
		srv := newQueryServer(t, &stubQueries{err: tc.err})
		for _, path := range []string{vehicles, vehicles + "/v1", vehicles + "/v1/actions", "/v1/dealerships/d-001/inventory/summary"} {
			got := do(t, srv, call{method: "GET", path: path})
			if got.status != tc.status || got.code() != tc.code {
				t.Errorf("%s with %v: %d %s", path, tc.err, got.status, got.rawBody)
			}
		}
	}
}
