package presentation

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"keyloop-coding-challenge/internal/application/command"
)

var (
	safeID      = regexp.MustCompile(`^[A-Za-z0-9._:@-]{1,128}$`)
	traceParent = regexp.MustCompile(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)
)

// commandMeta builds command metadata. X-Actor-Id is an UNAUTHENTICATED, demo-only
// identity: it is recorded for audit but never used for authorization.
func commandMeta(r *http.Request) (command.Meta, error) {
	actor := r.Header.Get("X-Actor-Id")
	if actor == "" {
		return command.Meta{}, badRequest("ACTOR_REQUIRED", "X-Actor-Id header is required on commands")
	}
	if !safeID.MatchString(actor) {
		return command.Meta{}, badRequest("INVALID_ACTOR", "X-Actor-Id must be 1-128 characters of A-Z a-z 0-9 . _ : @ -")
	}
	meta := command.Meta{
		ActorID:       actor,
		CorrelationID: requestIDFrom(r.Context()),
	}
	if tp := r.Header.Get("traceparent"); traceParent.MatchString(tp) {
		meta.TraceParent = tp
	}
	return meta, nil
}

// expectedVersion parses an optional If-Match: "<version>" (strong ETag).
func expectedVersion(r *http.Request) (*int, error) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return nil, nil
	}
	unquoted, err := strconv.Unquote(raw)
	if err != nil {
		unquoted = raw
	}
	v, err := strconv.Atoi(unquoted)
	if err != nil || v < 1 || strings.HasPrefix(raw, "W/") {
		return nil, badRequest("INVALID_IF_MATCH", `If-Match must be a strong ETag such as "3"`)
	}
	return &v, nil
}

// target reads the vehicle address from the URL and If-Match; the dealership always
// comes from the URL, never from the body.
func target(r *http.Request) (command.Target, error) {
	expected, err := expectedVersion(r)
	if err != nil {
		return command.Target{}, err
	}
	return command.Target{
		DealershipID:    r.PathValue("dealershipId"),
		VehicleID:       r.PathValue("vehicleId"),
		ExpectedVersion: expected,
	}, nil
}

func commandInputs(r *http.Request) (command.Meta, command.Target, error) {
	meta, err := commandMeta(r)
	if err != nil {
		return command.Meta{}, command.Target{}, err
	}
	t, err := target(r)
	return meta, t, err
}
