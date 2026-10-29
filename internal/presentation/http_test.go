package presentation_test

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/commandstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/referencedata"
	"keyloop-coding-challenge/internal/presentation"
	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type fixedClock struct{}

// 2026-10-29 in London (d-001).
func (fixedClock) Now() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) }

func newServer(t *testing.T) (*httptest.Server, *gorm.DB) {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&aggregates.VehicleInventoryAggregate{}); err != nil {
		t.Fatal(err)
	}
	store := commandstore.New(eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(db, serializer), 0))
	svc := command.NewService(store, referencedata.NewDealerships(db), fixedClock{}, discard)
	srv := httptest.NewServer(presentation.NewHandler(svc, nil, sqlDB.PingContext, discard))
	t.Cleanup(srv.Close)
	return srv, db
}

type call struct {
	method, path, body string
	headers            map[string]string
}

type reply struct {
	status  int
	header  http.Header
	body    map[string]interface{}
	rawBody string
}

func do(t *testing.T, srv *httptest.Server, c call) reply {
	t.Helper()
	req, err := http.NewRequest(c.method, srv.URL+c.path, strings.NewReader(c.body))
	if err != nil {
		t.Fatal(err)
	}
	if c.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Actor-Id", "manager-42")
	for k, v := range c.headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := reply{status: resp.StatusCode, header: resp.Header, rawBody: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func (r reply) code() string { s, _ := r.body["code"].(string); return s }

func stockBody(id string, stockedOn string) string {
	return fmt.Sprintf(`{"vehicle_id":%q,"vin":"1HGCM82633A004352","make":"Toyota","model":"Corolla","model_year":2023,"stocked_on":%q}`, id, stockedOn)
}

const vehicles = "/v1/dealerships/d-001/vehicles"

func TestWriteAPIHappyPath(t *testing.T) {
	srv, db := newServer(t)

	stocked := do(t, srv, call{method: "POST", path: vehicles, body: stockBody("v1", "2026-07-01"),
		headers: map[string]string{"X-Request-Id": "req-abc"}})
	if stocked.status != 201 || stocked.header.Get("Location") != vehicles+"/v1" || stocked.header.Get("ETag") != `"1"` {
		t.Fatalf("stock: %d %v %s", stocked.status, stocked.header, stocked.rawBody)
	}
	if stocked.header.Get("X-Request-Id") != "req-abc" || stocked.body["age_days"] != 120.0 || stocked.body["is_aging"] != true ||
		stocked.body["business_date"] != "2026-10-29" || stocked.body["current_action"] != nil {
		t.Fatalf("stock body: %s", stocked.rawBody)
	}

	proposed := do(t, srv, call{method: "POST", path: vehicles + "/v1/actions", body: `{"action_type":"PRICE_REDUCTION","note":"5%"}`})
	action, _ := proposed.body["current_action"].(map[string]interface{})
	if proposed.status != 201 || proposed.header.Get("ETag") != `"2"` || action == nil {
		t.Fatalf("propose: %d %s", proposed.status, proposed.rawBody)
	}
	actionID := action["action_id"].(string)
	if proposed.header.Get("Location") != vehicles+"/v1/actions/"+actionID {
		t.Fatalf("propose location %s", proposed.header.Get("Location"))
	}

	revised := do(t, srv, call{method: "PUT", path: vehicles + "/v1/actions/" + actionID, body: `{"action_type":"AUCTION","note":"lot 7"}`,
		headers: map[string]string{"If-Match": `"2"`}})
	if revised.status != 200 || revised.header.Get("ETag") != `"3"` {
		t.Fatalf("revise: %d %s", revised.status, revised.rawBody)
	}
	cancelled := do(t, srv, call{method: "POST", path: vehicles + "/v1/actions/" + actionID + "/cancel"})
	if cancelled.status != 200 || cancelled.body["current_action"] != nil || cancelled.header.Get("ETag") != `"4"` {
		t.Fatalf("cancel (empty body): %d %s", cancelled.status, cancelled.rawBody)
	}
	sold := do(t, srv, call{method: "POST", path: vehicles + "/v1/sale", body: `{"sold_on":"2026-10-29"}`})
	if sold.status != 200 || sold.body["status"] != "SOLD" || sold.body["sold_on"] != "2026-10-29" || sold.body["is_aging"] != false {
		t.Fatalf("sale: %d %s", sold.status, sold.rawBody)
	}

	var md string
	if err := db.Raw(`SELECT metadata FROM es_event WHERE aggregate_id = 'v1' AND version = 1`).Scan(&md).Error; err != nil {
		t.Fatal(err)
	}
	if md != `{"actor_id":"manager-42","correlation_id":"req-abc"}` {
		t.Fatalf("metadata %s", md)
	}
}

func TestWriteAPIErrors(t *testing.T) {
	srv, _ := newServer(t)
	do(t, srv, call{method: "POST", path: vehicles, body: stockBody("old", "2026-07-01")})   // 120 days
	do(t, srv, call{method: "POST", path: vehicles, body: stockBody("v90", "2026-07-31")})   // exactly 90 days
	do(t, srv, call{method: "POST", path: vehicles, body: stockBody("young", "2026-10-01")}) // 28 days

	cases := []struct {
		name   string
		call   call
		status int
		code   string
	}{
		{"missing actor", call{method: "POST", path: vehicles + "/old/actions", body: `{"action_type":"HOLD"}`, headers: map[string]string{"X-Actor-Id": ""}}, 400, "ACTOR_REQUIRED"},
		{"invalid actor", call{method: "POST", path: vehicles + "/old/actions", body: `{"action_type":"HOLD"}`, headers: map[string]string{"X-Actor-Id": "bad actor!"}}, 400, "INVALID_ACTOR"},
		{"unknown field", call{method: "POST", path: vehicles, body: `{"vin":"1HGCM82633A004352","dealership_id":"d-002"}`}, 400, "INVALID_BODY"},
		{"malformed json", call{method: "POST", path: vehicles, body: `{"vin":`}, 400, "INVALID_BODY"},
		{"trailing data", call{method: "POST", path: vehicles + "/old/sale", body: `{"sold_on":"2026-10-29"} {}`}, 400, "INVALID_BODY"},
		{"wrong type", call{method: "POST", path: vehicles, body: `{"model_year":"2023"}`}, 400, "VALIDATION_ERROR"},
		{"missing body", call{method: "POST", path: vehicles + "/old/sale", headers: map[string]string{"Content-Type": "application/json"}}, 400, "INVALID_BODY"},
		{"not json", call{method: "POST", path: vehicles + "/old/sale", body: `sold_on=x`, headers: map[string]string{"Content-Type": "text/plain"}}, 415, "UNSUPPORTED_MEDIA_TYPE"},
		{"too large", call{method: "POST", path: vehicles + "/old/actions", body: `{"action_type":"HOLD","note":"` + strings.Repeat("x", 70<<10) + `"}`}, 413, "BODY_TOO_LARGE"},
		{"bad date", call{method: "POST", path: vehicles + "/old/sale", body: `{"sold_on":"29/10/2026"}`}, 400, "VALIDATION_ERROR"},
		{"domain validation", call{method: "POST", path: vehicles, body: `{"vin":"BAD","make":"a","model":"b","model_year":2023,"stocked_on":"2026-01-01"}`}, 400, "VALIDATION_ERROR"},
		{"unknown dealership", call{method: "POST", path: "/v1/dealerships/d-404/vehicles", body: stockBody("z", "2026-07-01")}, 404, "DEALERSHIP_NOT_FOUND"},
		{"unknown vehicle", call{method: "POST", path: vehicles + "/nope/actions", body: `{"action_type":"HOLD"}`}, 404, "VEHICLE_NOT_FOUND"},
		{"other dealership's vehicle", call{method: "POST", path: "/v1/dealerships/d-002/vehicles/old/actions", body: `{"action_type":"HOLD"}`}, 404, "VEHICLE_NOT_FOUND"},
		{"duplicate vehicle id", call{method: "POST", path: vehicles, body: stockBody("old", "2026-07-01")}, 409, "VEHICLE_ALREADY_EXISTS"},
		{"not aging", call{method: "POST", path: vehicles + "/young/actions", body: `{"action_type":"HOLD"}`}, 409, "VEHICLE_NOT_AGING"},
		{"exactly 90 days", call{method: "POST", path: vehicles + "/v90/actions", body: `{"action_type":"HOLD"}`}, 409, "VEHICLE_NOT_AGING"},
		{"unknown action", call{method: "PUT", path: vehicles + "/old/actions/nope", body: `{"action_type":"HOLD"}`}, 409, "ACTION_NOT_ACTIVE"},
		{"stale If-Match", call{method: "POST", path: vehicles + "/old/actions", body: `{"action_type":"HOLD"}`, headers: map[string]string{"If-Match": `"5"`}}, 412, "VERSION_MISMATCH"},
		{"weak If-Match", call{method: "POST", path: vehicles + "/old/actions", body: `{"action_type":"HOLD"}`, headers: map[string]string{"If-Match": `W/"1"`}}, 400, "INVALID_IF_MATCH"},
		{"no such route", call{method: "GET", path: "/v1/nothing"}, 404, "NOT_FOUND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := do(t, srv, tc.call)
			if got.status != tc.status || got.code() != tc.code {
				t.Fatalf("got %d %s, want %d %s: %s", got.status, got.code(), tc.status, tc.code, got.rawBody)
			}
			if got.header.Get("Content-Type") != "application/problem+json" || got.body["request_id"] == "" || got.body["status"] != float64(tc.status) {
				t.Fatalf("not a problem response: %v %s", got.header, got.rawBody)
			}
		})
	}

	// The sale makes the vehicle unavailable for new actions.
	if r := do(t, srv, call{method: "POST", path: vehicles + "/old/sale", body: `{"sold_on":"2026-10-29"}`}); r.status != 200 {
		t.Fatalf("sale %d %s", r.status, r.rawBody)
	}
	after := do(t, srv, call{method: "POST", path: vehicles + "/old/actions", body: `{"action_type":"HOLD"}`})
	if after.status != 409 || after.code() != "VEHICLE_NOT_IN_STOCK" {
		t.Fatalf("after sale: %d %s", after.status, after.rawBody)
	}
}

// stubService returns a fixed error for every command.
type stubService struct{ err error }

func (s stubService) StockVehicle(context.Context, command.StockVehicle, command.Meta) (*command.Result, error) {
	return nil, s.err
}
func (s stubService) ProposeAction(context.Context, command.ProposeAction, command.Meta) (*command.Result, error) {
	return nil, s.err
}
func (s stubService) ReviseAction(context.Context, command.ReviseAction, command.Meta) (*command.Result, error) {
	return nil, s.err
}
func (s stubService) CancelAction(context.Context, command.CancelAction, command.Meta) (*command.Result, error) {
	return nil, s.err
}
func (s stubService) SellVehicle(context.Context, command.SellVehicle, command.Meta) (*command.Result, error) {
	return nil, s.err
}

func TestInfrastructureErrorsAreMappedWithoutLeakingDetails(t *testing.T) {
	secret := "Error 1045: Access denied for user 'root'@'10.0.0.1' (using password: YES) SELECT * FROM es_event"
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"connection lost", fmt.Errorf("%w: %s: %w", command.ErrUnavailable, secret, driver.ErrBadConn), 503, "UNAVAILABLE"},
		{"lock timeouts exhausted", fmt.Errorf("%s: %w", secret, command.ErrTransient), 503, "UNAVAILABLE"},
		{"deadline", fmt.Errorf("%s: %w", secret, context.DeadlineExceeded), 503, "UNAVAILABLE"},
		{"client went away", fmt.Errorf("%s: %w", secret, context.Canceled), 503, "UNAVAILABLE"},
		{"conflicts exhausted", fmt.Errorf("%s: %w", secret, command.ErrConflict), 409, "CONCURRENCY_CONFLICT"},
		// A version mismatch that also wraps a conflict must still be 412.
		{"If-Match race", fmt.Errorf("%w: %w", command.ErrVersionMismatch, command.ErrConflict), 412, "VERSION_MISMATCH"},
		{"unexpected", errors.New(secret), 500, "INTERNAL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(presentation.NewHandler(stubService{tc.err}, nil, nil, discard))
			defer srv.Close()
			got := do(t, srv, call{method: "POST", path: vehicles + "/v1/sale", body: `{"sold_on":"2026-10-29"}`})
			if got.status != tc.status || got.code() != tc.code {
				t.Fatalf("got %d %s: %s", got.status, got.code(), got.rawBody)
			}
			if strings.Contains(got.rawBody, "Access denied") || strings.Contains(got.rawBody, "SELECT") {
				t.Fatalf("internal detail leaked: %s", got.rawBody)
			}
		})
	}
}

func TestHealthAndReadiness(t *testing.T) {
	ready := func(ok bool) presentation.ReadinessCheck {
		return func(context.Context) error {
			if ok {
				return nil
			}
			return errors.New("dial tcp 127.0.0.1:3306: connection refused")
		}
	}
	for _, tc := range []struct {
		ok     bool
		status int
	}{{true, 200}, {false, 503}} {
		srv := httptest.NewServer(presentation.NewHandler(stubService{}, nil, ready(tc.ok), discard))
		if got := do(t, srv, call{method: "GET", path: "/readyz"}); got.status != tc.status || strings.Contains(got.rawBody, "3306") {
			t.Errorf("readyz ok=%v: %d %s", tc.ok, got.status, got.rawBody)
		}
		if got := do(t, srv, call{method: "GET", path: "/healthz"}); got.status != 200 {
			t.Errorf("healthz: %d", got.status)
		}
		srv.Close()
	}
}

func TestPanicIsRecovered(t *testing.T) {
	srv := httptest.NewServer(presentation.NewHandler(panicService{}, nil, nil, discard))
	defer srv.Close()
	got := do(t, srv, call{method: "POST", path: vehicles + "/v1/sale", body: `{"sold_on":"2026-10-29"}`})
	if got.status != 500 || got.code() != "INTERNAL" {
		t.Fatalf("got %d %s", got.status, got.rawBody)
	}
}

type panicService struct{ stubService }

func (panicService) SellVehicle(context.Context, command.SellVehicle, command.Meta) (*command.Result, error) {
	panic("boom")
}
