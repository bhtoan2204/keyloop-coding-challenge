//go:build integration

package presentation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/referencedata"
	"keyloop-coding-challenge/internal/presentation"
	"keyloop-coding-challenge/internal/testsupport/mysqltest"
	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/gorm"
)

type mysqlAPI struct {
	srv   *httptest.Server
	admin *gorm.DB
	store eventstore.AggregateStore // admin-side store for reloads
}

// newMySQLAPI serves the real handler and command service on MySQL, connected as the
// restricted application user from deploy/mysql/grants.sql. params are extra session
// variables for that connection.
func newMySQLAPI(t *testing.T, params map[string]string) *mysqlAPI {
	t.Helper()
	admin, cfg := mysqltest.NewMigratedDatabase(t)
	appCfg := mysqltest.AppUser(t, cfg)
	appCfg.Params = params
	app := mysqltest.Open(t, appCfg)

	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&aggregates.VehicleInventoryAggregate{}); err != nil {
		t.Fatal(err)
	}
	store := eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(app, serializer), 0)
	svc := command.NewService(store, referencedata.NewDealerships(app), fixedClock{}, discard)
	sqlDB, _ := app.DB()
	srv := httptest.NewServer(presentation.NewHandler(svc, sqlDB.PingContext, discard))
	t.Cleanup(srv.Close)
	return &mysqlAPI{srv: srv, admin: admin,
		store: eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(admin, serializer), 0)}
}

type eventRow struct {
	Version       int
	EventType     string
	AggregateType string
	Metadata      string
}

func (m *mysqlAPI) events(t *testing.T, id string) []eventRow {
	t.Helper()
	var rows []eventRow
	if err := m.admin.Raw(`SELECT version, event_type, aggregate_type, metadata FROM es_event WHERE aggregate_id = ? ORDER BY version`, id).
		Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func (m *mysqlAPI) eventTypes(t *testing.T, id string) []string {
	var types []string
	for _, e := range m.events(t, id) {
		types = append(types, e.EventType)
	}
	return types
}

func (m *mysqlAPI) stock(t *testing.T, id, stockedOn string) {
	t.Helper()
	if r := do(t, m.srv, call{method: "POST", path: vehicles, body: stockBody(id, stockedOn)}); r.status != 201 {
		t.Fatalf("stock %s: %d %s", id, r.status, r.rawBody)
	}
}

func TestMySQLCommandLifecycle(t *testing.T) {
	m := newMySQLAPI(t, nil)

	// 1, 13: stock persists VehicleStockedEvent with actor and correlation metadata.
	r := do(t, m.srv, call{method: "POST", path: vehicles, body: stockBody("v1", "2026-07-01"),
		headers: map[string]string{"X-Request-Id": "req-stock-1", "traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"}})
	if r.status != 201 {
		t.Fatalf("stock: %d %s", r.status, r.rawBody)
	}
	ev := m.events(t, "v1")
	if len(ev) != 1 || ev[0].EventType != "VehicleStockedEvent" || ev[0].AggregateType != "VehicleInventoryAggregate" {
		t.Fatalf("events %+v", ev)
	}
	var md map[string]string
	if err := json.Unmarshal([]byte(ev[0].Metadata), &md); err != nil {
		t.Fatal(err)
	}
	if md["actor_id"] != "manager-42" || md["correlation_id"] != "req-stock-1" || md["traceparent"] == "" {
		t.Fatalf("metadata %v", md)
	}

	// 2: aging vehicle accepts a proposal.
	r = do(t, m.srv, call{method: "POST", path: vehicles + "/v1/actions", body: `{"action_type":"PRICE_REDUCTION","note":"5%"}`})
	if r.status != 201 {
		t.Fatalf("propose: %d %s", r.status, r.rawBody)
	}
	actionID := r.body["current_action"].(map[string]interface{})["action_id"].(string)

	// 15: identical revision records nothing and keeps the version.
	r = do(t, m.srv, call{method: "PUT", path: vehicles + "/v1/actions/" + actionID, body: `{"action_type":"PRICE_REDUCTION","note":" 5% "}`})
	if r.status != 200 || r.header.Get("ETag") != `"2"` || len(m.events(t, "v1")) != 2 {
		t.Fatalf("no-op revise: %d %s", r.status, r.rawBody)
	}
	// 5: revision persists the new values.
	r = do(t, m.srv, call{method: "PUT", path: vehicles + "/v1/actions/" + actionID, body: `{"action_type":"AUCTION","note":"lot 7"}`})
	if r.status != 200 || r.header.Get("ETag") != `"3"` {
		t.Fatalf("revise: %d %s", r.status, r.rawBody)
	}
	// 6: cancellation.
	r = do(t, m.srv, call{method: "POST", path: vehicles + "/v1/actions/" + actionID + "/cancel", body: `{"reason":"buyer found"}`})
	if r.status != 200 || r.body["current_action"] != nil {
		t.Fatalf("cancel: %d %s", r.status, r.rawBody)
	}
	r = do(t, m.srv, call{method: "POST", path: vehicles + "/v1/actions", body: `{"action_type":"HOLD"}`})
	if r.status != 201 {
		t.Fatalf("second proposal: %d %s", r.status, r.rawBody)
	}
	lastResponse := r

	// 8: reloading from MySQL reproduces the committed state the API returned.
	reloaded := &aggregates.VehicleInventoryAggregate{}
	if err := m.store.Get(context.Background(), "v1", reloaded); err != nil {
		t.Fatal(err)
	}
	a, ok := reloaded.ActiveAction()
	current := lastResponse.body["current_action"].(map[string]interface{})
	if !ok || a.ID != current["action_id"] || reloaded.Version() != int(lastResponse.body["version"].(float64)) ||
		reloaded.VIN() != lastResponse.body["vin"] || string(reloaded.Status()) != lastResponse.body["status"] {
		t.Fatalf("reloaded state differs from response: %+v vs %s", a, lastResponse.rawBody)
	}

	// 7: after the sale the vehicle takes no new actions.
	r = do(t, m.srv, call{method: "POST", path: vehicles + "/v1/sale", body: `{"sold_on":"2026-10-29"}`})
	if r.status != 200 {
		t.Fatalf("sale: %d %s", r.status, r.rawBody)
	}
	r = do(t, m.srv, call{method: "POST", path: vehicles + "/v1/actions", body: `{"action_type":"HOLD"}`})
	if r.status != 409 || r.code() != "VEHICLE_NOT_IN_STOCK" {
		t.Fatalf("after sale: %d %s", r.status, r.rawBody)
	}
	want := []string{"VehicleStockedEvent", "VehicleActionProposedEvent", "VehicleActionRevisedEvent",
		"VehicleActionCancelledEvent", "VehicleActionProposedEvent", "VehicleSoldEvent"}
	if got := m.eventTypes(t, "v1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v", got)
	}
}

func TestMySQLCommandRejections(t *testing.T) {
	m := newMySQLAPI(t, nil)
	m.stock(t, "young", "2026-10-01") // 28 days
	m.stock(t, "v90", "2026-07-31")   // exactly 90 days
	m.stock(t, "old", "2026-07-01")   // 120 days

	cases := []struct {
		name   string
		call   call
		status int
		code   string
	}{
		{"3: not aging", call{method: "POST", path: vehicles + "/young/actions", body: `{"action_type":"HOLD"}`}, 409, "VEHICLE_NOT_AGING"},
		{"4: exactly 90 days", call{method: "POST", path: vehicles + "/v90/actions", body: `{"action_type":"HOLD"}`}, 409, "VEHICLE_NOT_AGING"},
		{"9: other dealership", call{method: "POST", path: "/v1/dealerships/d-002/vehicles/old/actions", body: `{"action_type":"HOLD"}`}, 404, "VEHICLE_NOT_FOUND"},
		{"10: stale If-Match", call{method: "POST", path: vehicles + "/old/actions", body: `{"action_type":"HOLD"}`, headers: map[string]string{"If-Match": `"2"`}}, 412, "VERSION_MISMATCH"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := do(t, m.srv, tc.call); got.status != tc.status || got.code() != tc.code {
				t.Fatalf("got %d %s", got.status, got.rawBody)
			}
		})
	}
	for _, id := range []string{"young", "v90", "old"} {
		if got := m.eventTypes(t, id); len(got) != 1 {
			t.Fatalf("%s: rejected commands wrote events: %v", id, got)
		}
	}
	// The matching If-Match succeeds.
	if got := do(t, m.srv, call{method: "POST", path: vehicles + "/old/actions", body: `{"action_type":"HOLD"}`, headers: map[string]string{"If-Match": `"1"`}}); got.status != 201 {
		t.Fatalf("current If-Match: %d %s", got.status, got.rawBody)
	}
}

// 11, 12: concurrent proposals on one aging vehicle. Losers retry by reloading, see the
// winner's action and are rejected by the domain rule — never by an unresolved conflict.
func TestMySQLConcurrentProposals(t *testing.T) {
	m := newMySQLAPI(t, nil)
	const requests = 16
	for round := 0; round < 5; round++ {
		id := fmt.Sprintf("c%d", round)
		m.stock(t, id, "2026-07-01")
		start := make(chan struct{})
		replies := make([]reply, requests)
		var wg sync.WaitGroup
		for i := range replies {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				replies[i] = do(t, m.srv, call{method: "POST", path: vehicles + "/" + id + "/actions", body: fmt.Sprintf(`{"action_type":"HOLD","note":"writer %d"}`, i)})
			}()
		}
		close(start)
		wg.Wait()
		created, alreadyActive := 0, 0
		for _, r := range replies {
			switch {
			case r.status == 201:
				created++
			case r.status == 409 && r.code() == "ACTION_ALREADY_ACTIVE":
				alreadyActive++
			default:
				t.Fatalf("round %d: unexpected %d %s", round, r.status, r.rawBody)
			}
		}
		if created != 1 || alreadyActive != requests-1 {
			t.Fatalf("round %d: created=%d alreadyActive=%d", round, created, alreadyActive)
		}
		ev := m.events(t, id)
		if len(ev) != 2 || ev[0].Version != 1 || ev[1].Version != 2 || ev[1].EventType != "VehicleActionProposedEvent" {
			t.Fatalf("round %d: events %+v", round, ev)
		}
	}
}

// 14: a save that cannot commit (row lock held, lock wait timeout every attempt) must
// not produce a success response or any event; the same request succeeds afterwards.
func TestMySQLFailedTransactionIsNotReportedAsSuccess(t *testing.T) {
	m := newMySQLAPI(t, map[string]string{"innodb_lock_wait_timeout": "1"})
	m.stock(t, "v1", "2026-07-01")

	sqlDB, err := m.admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	holder, err := sqlDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if _, err := holder.Exec(`SELECT version FROM es_aggregate WHERE id = 'v1' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}

	r := do(t, m.srv, call{method: "POST", path: vehicles + "/v1/actions", body: `{"action_type":"HOLD"}`})
	if r.status != 503 || r.code() != "UNAVAILABLE" || r.header.Get("ETag") != "" || r.header.Get("Retry-After") == "" {
		t.Fatalf("got %d %v %s", r.status, r.header, r.rawBody)
	}
	if got := m.eventTypes(t, "v1"); len(got) != 1 {
		t.Fatalf("failed command wrote events: %v", got)
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	if r := do(t, m.srv, call{method: "POST", path: vehicles + "/v1/actions", body: `{"action_type":"HOLD"}`}); r.status != 201 {
		t.Fatalf("after release: %d %s", r.status, r.rawBody)
	}
}
