package commandstore_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/commandstore"
	"keyloop-coding-challenge/package/eventsourcing"
)

// failingStore returns err from every call.
type failingStore struct{ err error }

func (f failingStore) Get(context.Context, string, eventsourcing.Aggregate) error { return f.err }
func (f failingStore) Save(context.Context, eventsourcing.Aggregate) error        { return f.err }
func (f failingStore) SaveWithMetadata(context.Context, eventsourcing.Aggregate, map[string]interface{}) error {
	return f.err
}

func TestErrorTranslation(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		want    error
		getOnly bool
	}{
		{"not found", fmt.Errorf("load: %w", eventstore.ErrAggregateNotFound), command.ErrStreamNotFound, true},
		{"conflict", fmt.Errorf("save: %w", eventstore.ErrConcurrencyConflict), command.ErrConflict, false},
		{"deadlock / lock wait", fmt.Errorf("save: %w", eventstore.ErrRetryable), command.ErrTransient, false},
		{"connection lost", fmt.Errorf("commit: %w", driver.ErrBadConn), command.ErrUnavailable, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := commandstore.New(failingStore{tc.err})
			got := s.Get(context.Background(), "v", nil)
			if !errors.Is(got, tc.want) || !errors.Is(got, tc.err) {
				t.Fatalf("Get: %v", got)
			}
			if tc.getOnly {
				return
			}
			got = s.SaveWithMetadata(context.Background(), nil, nil)
			if !errors.Is(got, tc.want) || !errors.Is(got, tc.err) {
				t.Fatalf("SaveWithMetadata: %v", got)
			}
		})
	}
	other := errors.New("corrupt stream")
	if got := commandstore.New(failingStore{other}).SaveWithMetadata(context.Background(), nil, nil); got != other {
		t.Fatalf("unrelated errors must pass through unchanged: %v", got)
	}
	if got := commandstore.New(failingStore{nil}).Get(context.Background(), "v", nil); got != nil {
		t.Fatalf("nil: %v", got)
	}
}
