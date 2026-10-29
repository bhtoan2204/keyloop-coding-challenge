package httpkit

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
)

// requestTimeout bounds every request's context, so a stuck database call ends with
// 503 instead of holding the connection until the server's write timeout.
const requestTimeout = 10 * time.Second

type ctxKey int

const requestIDKey ctxKey = iota

// RequestID returns the request's correlation ID (set by WithRequestContext).
func RequestID(ctx context.Context) string {
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

// WithRequestContext assigns the request ID (a valid inbound X-Request-Id is kept so
// callers can correlate), echoes it, applies the request timeout and writes one
// structured access-log line.
func WithRequestContext(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get("X-Request-Id")
		if !IsSafeID(id) {
			id = uuid.Must(uuid.NewV7()).String()
		}
		w.Header().Set("X-Request-Id", id)
		ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), requestIDKey, id), requestTimeout)
		defer cancel()
		r = r.WithContext(ctx)
		rec := &statusRecorder{
			ResponseWriter: w,
			status:         http.StatusOK,
		}
		next.ServeHTTP(rec, r)
		elapsed := time.Since(start)
		observeRequest(r, rec.status, elapsed)
		log.InfoContext(r.Context(), "http request",
			"request_id", id,
			"method", r.Method,
			"route", r.Pattern,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", elapsed.Milliseconds(),
			"actor_id", sanitizedActor(r))
	})
}

// WithRecovery turns a panic into a logged 500 problem response. http.ErrAbortHandler
// is re-raised: it is net/http's signal to abort the response.
func WithRecovery(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.ErrorContext(r.Context(), "panic", "panic", v, "stack", string(debug.Stack()))
				WriteError(w, r, log, errors.New("panic"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// sanitizedActor keeps an unvalidated header out of the logs unless it is a safe ID.
func sanitizedActor(r *http.Request) string {
	if actor := r.Header.Get("X-Actor-Id"); IsSafeID(actor) {
		return actor
	}
	return ""
}
