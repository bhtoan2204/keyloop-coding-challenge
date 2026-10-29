package presentation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
)

// problem is an RFC 9457 problem details body with a stable machine-readable code.
type problem struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Status    int          `json:"status"`
	Code      string       `json:"code"`
	Detail    string       `json:"detail,omitempty"`
	Instance  string       `json:"instance,omitempty"`
	RequestID string       `json:"request_id,omitempty"`
	Errors    []fieldError `json:"errors,omitempty"`
}

type fieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// apiError is a request-level error raised by the HTTP layer itself (bad JSON,
// missing header, ...). Its detail is safe to show to the client.
type apiError struct {
	status int
	code   string
	detail string
	fields []fieldError
}

func (e *apiError) Error() string { return e.code + ": " + e.detail }

func badRequest(code, detail string) *apiError {
	return &apiError{status: http.StatusBadRequest, code: code, detail: detail}
}

func invalidField(field, message string) *apiError {
	return &apiError{status: http.StatusBadRequest, code: "VALIDATION_ERROR", detail: "request validation failed",
		fields: []fieldError{{Field: field, Message: message}}}
}

// domainErrors maps known business and application errors to HTTP. Their messages
// contain only identifiers and dates from the request, so they are returned as detail.
var domainErrors = []struct {
	err    error
	status int
	code   string
	title  string
}{
	{command.ErrDealershipNotFound, http.StatusNotFound, "DEALERSHIP_NOT_FOUND", "Dealership not found"},
	{command.ErrVehicleNotFound, http.StatusNotFound, "VEHICLE_NOT_FOUND", "Vehicle not found"},
	{domain.ErrVehicleNotStocked, http.StatusNotFound, "VEHICLE_NOT_FOUND", "Vehicle not found"},
	{command.ErrVersionMismatch, http.StatusPreconditionFailed, "VERSION_MISMATCH", "Vehicle version does not match If-Match"},
	{domain.ErrVehicleAlreadyStocked, http.StatusConflict, "VEHICLE_ALREADY_EXISTS", "Vehicle already exists"},
	{domain.ErrVehicleNotInStock, http.StatusConflict, "VEHICLE_NOT_IN_STOCK", "Vehicle is not in stock"},
	{domain.ErrVehicleAlreadySold, http.StatusConflict, "VEHICLE_ALREADY_SOLD", "Vehicle is already sold"},
	{domain.ErrVehicleNotAging, http.StatusConflict, "VEHICLE_NOT_AGING", "Vehicle is not aging"},
	{domain.ErrActionAlreadyActive, http.StatusConflict, "ACTION_ALREADY_ACTIVE", "An action is already active"},
	{domain.ErrActionNotActive, http.StatusConflict, "ACTION_NOT_ACTIVE", "Action is not active"},
}

// writeError maps err to a problem response. Unexpected errors are logged in full and
// answered with a generic message: no SQL or internal detail reaches the client.
func writeError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	p := problem{Instance: r.URL.Path, RequestID: requestIDFrom(r.Context())}
	var apiErr *apiError
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &apiErr):
		p.Status, p.Code, p.Title, p.Detail, p.Errors = apiErr.status, apiErr.code, http.StatusText(apiErr.status), apiErr.detail, apiErr.fields
	case errors.As(err, &validation):
		p.Status, p.Code, p.Title, p.Detail = http.StatusBadRequest, "VALIDATION_ERROR", "Request validation failed", "request validation failed"
		p.Errors = []fieldError{{Field: validation.Field, Message: validation.Reason}}
	case errors.Is(err, eventstore.ErrConcurrencyConflict):
		p.Status, p.Code, p.Title = http.StatusConflict, "CONCURRENCY_CONFLICT", "Concurrent update"
		p.Detail = "the vehicle kept changing while the command was retried; reload and try again"
	case errors.Is(err, eventstore.ErrRetryable), eventstore.IsUnavailable(err), errors.Is(err, context.DeadlineExceeded):
		p.Status, p.Code, p.Title = http.StatusServiceUnavailable, "UNAVAILABLE", "Service temporarily unavailable"
		p.Detail = "the command was not confirmed; check the vehicle's state before retrying"
		w.Header().Set("Retry-After", "1")
	default:
		for _, m := range domainErrors {
			if errors.Is(err, m.err) {
				p.Status, p.Code, p.Title, p.Detail = m.status, m.code, m.title, err.Error()
				break
			}
		}
		if p.Status == 0 {
			p.Status, p.Code, p.Title = http.StatusInternalServerError, "INTERNAL", "Internal server error"
		}
	}
	level := slog.LevelInfo
	if p.Status >= 500 {
		level = slog.LevelError
	}
	log.Log(r.Context(), level, "request failed", "status", p.Status, "code", p.Code, "error", err.Error())
	p.Type = "https://errors.inventory.local/" + p.Code
	writeJSON(w, p.Status, "application/problem+json", p)
}

func writeJSON(w http.ResponseWriter, status int, contentType string, body interface{}) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
