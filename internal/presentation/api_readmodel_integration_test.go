//go:build integration

package presentation_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/commandstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/referencedata"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/internal/presentation"
	"keyloop-coding-challenge/internal/testsupport/mysqltest"
	"keyloop-coding-challenge/internal/testsupport/pgtest"
	"keyloop-coding-challenge/package/eventsourcing"
)

// TestEndToEndWriteSyncQuery: commands go to MySQL, the read model is refreshed by the
// Phase 4 sync, queries read PostgreSQL. Until a sync runs, queries do not see a new
// command's effect (documented eventual consistency).
func TestEndToEndWriteSyncQuery(t *testing.T) {
	_, mysqlCfg := mysqltest.NewMigratedDatabase(t)
	app := mysqltest.Open(t, mysqltest.AppUser(t, mysqlCfg))
	pg, _ := pgtest.NewDatabase(t)
	if err := viewstore.Migrate(pg); err != nil {
		t.Fatal(err)
	}

	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&aggregates.VehicleInventoryAggregate{}); err != nil {
		t.Fatal(err)
	}
	store := commandstore.New(eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(app, serializer), 0))
	commands := command.NewService(store, referencedata.NewDealerships(app), fixedClock{}, discard)
	readModel := viewstore.NewPostgres(pg)
	queries := query.NewService(readModel, fixedClock{})
	srv := httptest.NewServer(presentation.NewHandler(commands, queries, nil, discard))
	t.Cleanup(srv.Close)
	runner := projection.NewRunner(repos.New(app, serializer).EventStore(), readModel, &projectors.VehicleProjector{})
	sync := func(wantApplied int) {
		t.Helper()
		res, err := runner.Sync(context.Background())
		if err != nil || res.Applied != wantApplied {
			t.Fatalf("sync: %+v %v (want %d applied)", res, err, wantApplied)
		}
	}
	get := func(path string) reply {
		t.Helper()
		return do(t, srv, call{method: "GET", path: path})
	}
	count := func(r reply) int { return len(r.body["data"].([]interface{})) }

	for _, body := range []string{stockBody("old", "2026-07-01"), stockBody("young", "2026-10-01")} {
		if r := do(t, srv, call{method: "POST", path: vehicles, body: body}); r.status != 201 {
			t.Fatalf("stock: %d %s", r.status, r.rawBody)
		}
	}
	if n := count(get(vehicles)); n != 0 {
		t.Fatalf("read model changed before sync: %d vehicles", n)
	}
	sync(2)

	list := get(vehicles)
	if count(list) != 2 || list.body["data"].([]interface{})[0].(map[string]interface{})["vehicle_id"] != "old" {
		t.Fatalf("list: %s", list.rawBody)
	}
	aging := get(vehicles + "?aging=true")
	if count(aging) != 1 {
		t.Fatalf("aging: %s", aging.rawBody)
	}
	detail := get(vehicles + "/old")
	if detail.body["age_days"] != 120.0 || detail.body["is_aging"] != true || detail.header.Get("ETag") != `"1"` {
		t.Fatalf("detail: %s", detail.rawBody)
	}

	if r := do(t, srv, call{method: "POST", path: vehicles + "/old/actions", body: `{"action_type":"PRICE_REDUCTION","note":"5%"}`,
		headers: map[string]string{"X-Request-Id": "req-e2e"}}); r.status != 201 {
		t.Fatalf("propose: %d %s", r.status, r.rawBody)
	}
	if d := get(vehicles + "/old"); d.body["current_action"] != nil || d.body["version"] != 1.0 {
		t.Fatalf("action visible before sync: %s", d.rawBody)
	}
	sync(1)
	detail = get(vehicles + "/old")
	action, _ := detail.body["current_action"].(map[string]interface{})
	if action == nil || action["action_type"] != "PRICE_REDUCTION" || action["updated_by"] != "manager-42" || detail.body["version"] != 2.0 {
		t.Fatalf("detail after sync: %s", detail.rawBody)
	}
	history := get(vehicles + "/old/actions")
	entry := history.body["data"].([]interface{})[0].(map[string]interface{})
	if count(history) != 1 || entry["change"] != "PROPOSED" || entry["correlation_id"] != "req-e2e" {
		t.Fatalf("history: %s", history.rawBody)
	}
	sum := get("/v1/dealerships/d-001/inventory/summary")
	if sum.body["in_stock"] != 2.0 || sum.body["aging"] != 1.0 || sum.body["aging_without_action"] != 0.0 {
		t.Fatalf("summary: %s", sum.rawBody)
	}
	if r := get("/v1/dealerships/d-002/vehicles/old"); r.status != 404 || r.code() != "VEHICLE_NOT_FOUND" {
		t.Fatalf("cross-dealership: %d %s", r.status, r.rawBody)
	}

	// A full rebuild from MySQL yields the same answers.
	before := get(vehicles + "?status=ALL").rawBody
	if _, err := runner.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := get(vehicles + "?status=ALL").rawBody; after != before {
		t.Fatalf("rebuild changed the read model\nbefore %s\nafter  %s", before, after)
	}

	// Selling removes the vehicle from aging stock (after the next sync).
	if r := do(t, srv, call{method: "POST", path: vehicles + "/old/sale", body: `{"sold_on":"2026-10-29"}`}); r.status != 200 {
		t.Fatalf("sale: %d %s", r.status, r.rawBody)
	}
	sync(1)
	if n := count(get(vehicles + "?aging=true")); n != 0 {
		t.Fatalf("sold vehicle still aging: %d", n)
	}
	if n := count(get(vehicles + "?status=SOLD")); n != 1 {
		t.Fatalf("sold list: %d", n)
	}
	history = get(vehicles + "/old/actions")
	if count(history) != 2 || history.body["data"].([]interface{})[1].(map[string]interface{})["change"] != "CLOSED_BY_SALE" {
		t.Fatalf("history after sale: %s", history.rawBody)
	}
}
