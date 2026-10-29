package presentation

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
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
	return &apiError{
		status: http.StatusBadRequest,
		code:   "VALIDATION_ERROR",
		detail: "request validation failed",
		fields: []fieldError{{Field: field, Message: message}},
	}
}

// writeError maps err to a problem response. Unexpected errors are logged in full and
// answered with a generic message: no SQL or internal detail reaches the client.
func writeError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	p := problemFor(err)
	p.Instance = r.URL.Path
	p.RequestID = requestIDFrom(r.Context())
	p.Type = "https://errors.inventory.local/" + p.Code
	if p.Code == codeUnavailable {
		w.Header().Set("Retry-After", "1")
	}
	level := slog.LevelInfo
	if p.Status >= 500 && !errors.Is(err, context.Canceled) { // a client that went away is not a server error
		level = slog.LevelError
	}
	log.Log(r.Context(), level, "request failed", "status", p.Status, "code", p.Code, "error", err.Error())
	writeJSON(w, p.Status, "application/problem+json", p)
}
