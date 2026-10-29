package presentation_test

import (
	"context"
	"errors"
	"net/http/httptest"
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
