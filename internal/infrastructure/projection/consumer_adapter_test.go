package projection_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"keyloop-coding-challenge/internal/application/messaging"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/package/eventsourcing"
)

type stubProjection struct {
	applied bool
	err     error
}

func (s stubProjection) Apply(context.Context, eventsourcing.Event) (bool, error) {
	return s.applied, s.err
}

// TestConsumerAdapterTranslatesErrors pins the error contract the live consumer
// relies on: a gap is repairable, a rejected event is poison, anything else is
// transient, and the original error stays in the chain.
func TestConsumerAdapterTranslatesErrors(t *testing.T) {
	transient := errors.New("connection refused")
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"gap", fmt.Errorf("x: %w", projection.ErrVersionGap), messaging.ErrVersionGap},
		{"schema", projection.ErrUnsupportedSchemaVersion, messaging.ErrRejectedEvent},
		{"unsupported", fmt.Errorf("projector: %w", projectors.ErrUnsupportedEvent), messaging.ErrRejectedEvent},
		{"inconsistent", fmt.Errorf("projector: %w", projectors.ErrInconsistent), messaging.ErrRejectedEvent},
		{"transient", transient, transient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := projection.NewConsumerAdapter(stubProjection{err: tc.err}, nil, nil)
			applied, err := a.Apply(context.Background(), eventsourcing.Event{})
			if applied || !errors.Is(err, tc.want) || !errors.Is(err, tc.err) {
				t.Fatalf("got %v, %v; want %v wrapping %v", applied, err, tc.want, tc.err)
			}
			if tc.want == transient && (errors.Is(err, messaging.ErrVersionGap) || errors.Is(err, messaging.ErrRejectedEvent)) {
				t.Fatalf("transient error classified: %v", err)
			}
		})
	}
	a := projection.NewConsumerAdapter(stubProjection{applied: true}, nil, nil)
	if applied, err := a.Apply(context.Background(), eventsourcing.Event{}); !applied || err != nil {
		t.Fatalf("success: %v %v", applied, err)
	}
}

type stubStream struct{ err error }

func (s stubStream) ReadStream(context.Context, string, int, int) ([]eventsourcing.Event, error) {
	return nil, s.err
}

// TestConsumerAdapterClassifiesUndecodableHistory: a gap repair that meets an event
// this build cannot decode is poison, like the same event arriving from Kafka.
func TestConsumerAdapterClassifiesUndecodableHistory(t *testing.T) {
	transient := errors.New("connection refused")
	cases := []struct {
		err  error
		want error
	}{
		{fmt.Errorf("read stream v1: %w x_y", repos.ErrUnknownEventType), messaging.ErrUnknownEventType},
		{fmt.Errorf("read stream v1: %w", repos.ErrUnsupportedSchemaVersion), messaging.ErrUnsupportedSchemaVersion},
		{transient, transient},
	}
	for _, tc := range cases {
		a := projection.NewConsumerAdapter(nil, nil, stubStream{err: tc.err})
		_, err := a.ReadStream(context.Background(), "v1", 0, 2)
		if !errors.Is(err, tc.want) || !errors.Is(err, tc.err) {
			t.Errorf("%v: got %v, want %v", tc.err, err, tc.want)
		}
	}
}
