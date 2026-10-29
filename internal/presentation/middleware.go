package presentation

import (
	"context"
	"errors"
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
