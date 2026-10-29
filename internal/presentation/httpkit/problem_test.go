package httpkit_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/presentation/httpkit"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type problemBody struct {
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	Instance  string `json:"instance"`
	RequestID string `json:"request_id"`
}

func writeError(t *testing.T, err error) (*httptest.ResponseRecorder, problemBody) {
	t.Helper()
	rec := httptest.NewRecorder()
	httpkit.WriteError(rec, httptest.NewRequest("POST", "/v1/x", nil), discard, err)
	var body problemBody
	if e := json.Unmarshal(rec.Body.Bytes(), &body); e != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), e)
	}
	return rec, body
}

func TestWriteErrorMapping(t *testing.T) {
	wrap := func(err error) error { return fmt.Errorf("context: %w", err) }
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{httpkit.BadRequest("ACTOR_REQUIRED", "x"), 400, "ACTOR_REQUIRED"},
		{httpkit.InvalidField("vin", "is required"), 400, "VALIDATION_ERROR"},
		{domain.Invalid("vin", "is required"), 400, "VALIDATION_ERROR"},
		{wrap(command.ErrVersionMismatch), 412, "VERSION_MISMATCH"},
		// A stale If-Match wins even when the store also reported a conflict.
		{fmt.Errorf("%w: %w", command.ErrConflict, command.ErrVersionMismatch), 412, "VERSION_MISMATCH"},
		{wrap(command.ErrConflict), 409, "CONCURRENCY_CONFLICT"},
		{wrap(command.ErrTransient), 503, "UNAVAILABLE"},
		{context.DeadlineExceeded, 503, "UNAVAILABLE"},
		{wrap(command.ErrDealershipNotFound), 404, "DEALERSHIP_NOT_FOUND"},
		{wrap(query.ErrVehicleNotFound), 404, "VEHICLE_NOT_FOUND"},
		{wrap(query.ErrInvalidCursor), 400, "INVALID_CURSOR"},
		{wrap(domain.ErrActionAlreadyActive), 409, "ACTION_ALREADY_ACTIVE"},
		{errors.New("pq: connection refused at 10.0.0.5"), 500, "INTERNAL"},
	}
	for _, tc := range cases {
		rec, body := writeError(t, tc.err)
		if rec.Code != tc.status || body.Status != tc.status || body.Code != tc.code {
			t.Errorf("%v: got %d %s, want %d %s", tc.err, rec.Code, body.Code, tc.status, tc.code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("%v: Content-Type %q", tc.err, ct)
		}
	}
}

func TestWriteErrorHidesInternalDetailAndSetsRetryAfter(t *testing.T) {
	rec, body := writeError(t, errors.New("pq: relation inventory_vehicle: connection refused"))
	if body.Detail != "" || strings.Contains(rec.Body.String(), "pq:") {
		t.Fatalf("internal detail leaked: %s", rec.Body.String())
	}
	if body.Instance != "/v1/x" {
		t.Fatalf("instance %q", body.Instance)
	}
	rec, _ = writeError(t, command.ErrUnavailable)
	if rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("503 without Retry-After: %v", rec.Header())
	}
}
