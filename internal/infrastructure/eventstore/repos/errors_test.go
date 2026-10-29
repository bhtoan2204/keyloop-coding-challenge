package repos

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestClassifyTxError(t *testing.T) {
	wrapped := func(n uint16) error {
		return fmt.Errorf("append event: %w", &mysql.MySQLError{Number: n, Message: "x"})
	}
	cases := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"deadlock", wrapped(mysqlDeadlock), true},
		{"lock wait timeout", wrapped(mysqlLockWaitTimeout), true},
		{"duplicate entry", wrapped(mysqlDuplicateEntry), false},
		{"other mysql error", wrapped(1146), false},
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
			if errors.Is(got, ErrConcurrencyConflict) != errors.Is(tc.err, ErrConcurrencyConflict) {
				t.Fatalf("conflict classification changed: %v", got)
			}
		})
	}
}

func TestIsDuplicateKey(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"mysql 1062":         {&mysql.MySQLError{Number: mysqlDuplicateEntry}, true},
		"mysql other number": {&mysql.MySQLError{Number: mysqlDeadlock, Message: "Duplicate entry in text"}, false},
		"sqlite unique":      {errors.New("UNIQUE constraint failed: es_event.aggregate_id"), true},
		"unrelated":          {errors.New("connection refused"), false},
		"nil":                {nil, false},
	}
	for name, tc := range cases {
		if got := isDuplicateKey(tc.err); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}
