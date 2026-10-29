package presentation_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"keyloop-coding-challenge/internal/application/messaging"
	"keyloop-coding-challenge/internal/presentation"
)

func TestProbeHandler(t *testing.T) {
	var notReady error = errors.New("partition 0 halted")
	status := messaging.Status{
		Phase:         messaging.PhaseConsuming,
		EventsApplied: 7,
	}
	srv := httptest.NewServer(presentation.NewProbeHandler(
		func(context.Context) error { return notReady },
		func() messaging.Status { return status },
		discard,
	))
	t.Cleanup(srv.Close)

	if r := do(t, srv, call{method: "GET", path: "/healthz"}); r.status != 200 {
		t.Fatalf("healthz: %d", r.status)
	}
	if r := do(t, srv, call{method: "GET", path: "/readyz"}); r.status != 503 || r.body["status"] != "unavailable" {
		t.Fatalf("readyz while halted: %d %s", r.status, r.rawBody)
	}
	notReady = nil
	if r := do(t, srv, call{method: "GET", path: "/readyz"}); r.status != 200 {
		t.Fatalf("readyz when ready: %d %s", r.status, r.rawBody)
	}
	r := do(t, srv, call{method: "GET", path: "/status"})
	if r.status != 200 || r.body["phase"] != "consuming" || r.body["events_applied"] != 7.0 {
		t.Fatalf("status: %d %s", r.status, r.rawBody)
	}
	if r := do(t, srv, call{method: "POST", path: "/v1/dealerships/d-001/vehicles"}); r.status != 404 || r.code() != "NOT_FOUND" {
		t.Fatalf("command routes must not exist on the probe server: %d %s", r.status, r.rawBody)
	}
}

func TestMetricsEndpoints(t *testing.T) {
	api := httptest.NewServer(presentation.NewHandler(stubService{}, &stubQueries{}, nil, discard))
	t.Cleanup(api.Close)
	do(t, api, call{method: "GET", path: vehicles + "?agng=true"})
	body := do(t, api, call{method: "GET", path: "/metrics"}).rawBody
	for _, want := range []string{
		`inventory_http_requests_total{method="GET",route="GET /v1/dealerships/{dealershipId}/vehicles",status="400"}`,
		`inventory_http_problems_total{code="INVALID_QUERY",route="GET /v1/dealerships/{dealershipId}/vehicles"}`,
		`inventory_http_request_duration_seconds_bucket{method="GET",route="GET /v1/dealerships/{dealershipId}/vehicles"`,
		"go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("API /metrics lacks %s", want)
		}
	}

	probe := httptest.NewServer(presentation.NewProbeHandler(nil, func() messaging.Status {
		return messaging.Status{
			Phase:         messaging.PhaseConsuming,
			EventsApplied: 5,
		}
	}, discard))
	t.Cleanup(probe.Close)
	body = do(t, probe, call{method: "GET", path: "/metrics"}).rawBody
	for _, want := range []string{"inventory_projection_events_applied_total 5", `inventory_projection_phase{phase="consuming"} 1`} {
		if !strings.Contains(body, want) {
			t.Errorf("projector /metrics lacks %s", want)
		}
	}
	// A second probe handler in the same process must not panic on registration.
	presentation.NewProbeHandler(nil, func() messaging.Status { return messaging.Status{} }, discard)
}
