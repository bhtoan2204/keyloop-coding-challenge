// Package presentation is the REST API. It translates HTTP into application commands
// and application errors into RFC 9457 problem responses; it holds no business rules.
package presentation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"keyloop-coding-challenge/internal/application/command"

	"github.com/google/uuid"
)

// maxBodyBytes limits command request bodies; the largest valid body is a few KB.
const maxBodyBytes = 64 << 10

// requestTimeout bounds every request's context, so a stuck database call ends with
// 503 instead of holding the connection until the server's write timeout.
const requestTimeout = 10 * time.Second

// CommandService is implemented by *command.Service.
type CommandService interface {
	StockVehicle(ctx context.Context, cmd command.StockVehicle, meta command.Meta) (*command.Result, error)
	ProposeAction(ctx context.Context, cmd command.ProposeAction, meta command.Meta) (*command.Result, error)
	ReviseAction(ctx context.Context, cmd command.ReviseAction, meta command.Meta) (*command.Result, error)
	CancelAction(ctx context.Context, cmd command.CancelAction, meta command.Meta) (*command.Result, error)
	SellVehicle(ctx context.Context, cmd command.SellVehicle, meta command.Meta) (*command.Result, error)
}

// ReadinessCheck reports whether the dependencies of the write API are reachable.
type ReadinessCheck func(ctx context.Context) error

type api struct {
	commands CommandService
	ready    ReadinessCheck
	log      *slog.Logger
}

// NewHandler returns the HTTP handler with all routes and middleware.
func NewHandler(commands CommandService, ready ReadinessCheck, log *slog.Logger) http.Handler {
	a := &api{commands: commands, ready: ready, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /readyz", a.readyz)

	const vehicle = "/v1/dealerships/{dealershipId}/vehicles/{vehicleId}"
	mux.HandleFunc("POST /v1/dealerships/{dealershipId}/vehicles", a.stockVehicle)
	mux.HandleFunc("POST "+vehicle+"/actions", a.proposeAction)
	mux.HandleFunc("PUT "+vehicle+"/actions/{actionId}", a.reviseAction)
	mux.HandleFunc("POST "+vehicle+"/actions/{actionId}/cancel", a.cancelAction)
	mux.HandleFunc("POST "+vehicle+"/sale", a.sellVehicle)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, a.log, &apiError{status: http.StatusNotFound, code: "NOT_FOUND", detail: "no such endpoint"})
	})
	return a.withRequestContext(a.withRecovery(mux))
}

func (a *api) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, "application/json", map[string]string{"status": "ok"})
}

func (a *api) readyz(w http.ResponseWriter, r *http.Request) {
	if a.ready == nil {
		writeJSON(w, http.StatusServiceUnavailable, "application/json", map[string]string{"status": "unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.ready(ctx); err != nil {
		a.log.WarnContext(r.Context(), "not ready", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, "application/json", map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, "application/json", map[string]string{"status": "ok"})
}

// --- request context: request ID, actor, logging ------------------------------------

type ctxKey int

const requestIDKey ctxKey = iota

var (
	safeID      = regexp.MustCompile(`^[A-Za-z0-9._:@-]{1,128}$`)
	traceParent = regexp.MustCompile(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)
)

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// withRequestContext assigns the request ID (a valid inbound X-Request-Id is kept so
// callers can correlate), echoes it, and writes one structured access-log line.
func (a *api) withRequestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get("X-Request-Id")
		if !safeID.MatchString(id) {
			id = uuid.Must(uuid.NewV7()).String()
		}
		w.Header().Set("X-Request-Id", id)
		ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), requestIDKey, id), requestTimeout)
		defer cancel()
		r = r.WithContext(ctx)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		a.log.InfoContext(r.Context(), "http request",
			"request_id", id, "method", r.Method, "route", r.Pattern, "path", r.URL.Path,
			"status", rec.status, "duration_ms", time.Since(start).Milliseconds(),
			"actor_id", sanitizedActor(r))
	})
}

func (a *api) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				a.log.ErrorContext(r.Context(), "panic", "panic", v, "stack", string(debug.Stack()))
				writeError(w, r, a.log, errors.New("panic"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func sanitizedActor(r *http.Request) string {
	if actor := r.Header.Get("X-Actor-Id"); safeID.MatchString(actor) {
		return actor
	}
	return ""
}

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
	meta := command.Meta{ActorID: actor, CorrelationID: requestIDFrom(r.Context())}
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

// decodeJSON strictly decodes a single JSON object: application/json only, bounded
// size, unknown fields rejected, no trailing data. An empty body is allowed only when
// allowEmpty is set.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}, allowEmpty bool) error {
	if r.ContentLength == 0 && allowEmpty && r.Header.Get("Content-Type") == "" {
		return nil
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return &apiError{status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE", detail: "Content-Type must be application/json"}
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.As(err, &tooLarge):
			return &apiError{status: http.StatusRequestEntityTooLarge, code: "BODY_TOO_LARGE", detail: "request body exceeds 64 KiB"}
		case errors.Is(err, io.EOF):
			if allowEmpty {
				return nil
			}
			return badRequest("INVALID_BODY", "request body is required")
		case errors.As(err, &typeErr):
			field := typeErr.Field
			if field == "" {
				field = "body" // the top-level value is not a JSON object
			}
			return invalidField(field, "has the wrong type")
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			return badRequest("INVALID_BODY", strings.TrimPrefix(err.Error(), "json: "))
		default:
			return badRequest("INVALID_BODY", "request body is not valid JSON")
		}
	}
	if dec.More() {
		return badRequest("INVALID_BODY", "request body must contain a single JSON object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return badRequest("INVALID_BODY", "request body must contain a single JSON object")
	}
	return nil
}
