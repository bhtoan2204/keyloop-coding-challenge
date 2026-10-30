package repos

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func pgErr(code string) error {
	return fmt.Errorf("append event: %w", &pgconn.PgError{Code: code, Message: "x"})
}

func TestClassifyTxError(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"serialization failure", pgErr(sqlstateSerializationFailure), true},
		{"deadlock", pgErr(sqlstateDeadlockDetected), true},
		{"lock not available", pgErr(sqlstateLockNotAvailable), true},
		{"unique violation", pgErr(sqlstateUniqueViolation), false},
		{"undefined table", pgErr("42P01"), false},
		{"optimistic conflict", fmt.Errorf("x: %w", ErrConcurrencyConflict), false},
		{"context canceled", fmt.Errorf("x: %w", context.Canceled), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyTxError(tc.err)
			if errors.Is(got, ErrRetryable) != tc.retryable {
				t.Fatalf("classifyTxError(%v) = %v, retryable want %v", tc.err, got, tc.retryable)
			}
			if tc.err != nil && !errors.Is(got, tc.err) {
				t.Fatalf("original error lost: %v", got)
			}
		})
	}
}

func TestIsDuplicateKey(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{pgErr(sqlstateUniqueViolation), true},
		{pgErr("23503"), false}, // foreign key violation is not a duplicate
		{errors.New("UNIQUE constraint failed: es_event.aggregate_id, es_event.version"), true},
		{errors.New("no such table"), false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := isDuplicateKey(tc.err); got != tc.want {
			t.Errorf("isDuplicateKey(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// TestIsUnavailable: these errors leave the outcome of a COMMIT unknown, so they must
// not be classified as retryable.
func TestIsUnavailable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"bad conn", driver.ErrBadConn, true},
		{"conn done", fmt.Errorf("commit: %w", sql.ErrConnDone), true},
		{"unexpected EOF", fmt.Errorf("commit: %w", io.ErrUnexpectedEOF), true},
		{"network", &net.OpError{Op: "read", Err: errors.New("connection reset")}, true},
		{"connect", &pgconn.ConnectError{}, true},
		{"connection exception class", pgErr("08006"), true},
		{"admin shutdown", pgErr(sqlstateAdminShutdown), true},
		{"query canceled", pgErr(sqlstateQueryCanceled), true},
		{"deadlock", pgErr(sqlstateDeadlockDetected), false},
		{"unique violation", pgErr(sqlstateUniqueViolation), false},
		{"conflict", ErrConcurrencyConflict, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUnavailable(tc.err); got != tc.want {
				t.Fatalf("IsUnavailable(%v) = %v, want %v", tc.err, got, tc.want)
			}
			if tc.want && errors.Is(classifyTxError(tc.err), ErrRetryable) {
				t.Fatalf("%v is unavailable but classified retryable", tc.err)
			}
		})
	}
}
