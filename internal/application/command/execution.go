package command

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/aggregates"
)

// MaxAttempts bounds how often a command is re-run after a retryable save failure.
const MaxAttempts = 3

// execute loads the vehicle, runs the domain command against the current state and
// saves the new events. When the save fails with a retryable error (optimistic
// conflict, deadlock, lock wait timeout) it reloads and re-runs the command, so the
// decision is always made on fresh state; stale pending events are never re-sent.
// An If-Match version is re-checked on every attempt, so a competing commit ends in
// ErrVersionMismatch rather than a retry on newer state. Domain rejections and
// ErrUnavailable (commit outcome unknown) are never retried.
func (s *Service) execute(ctx context.Context, target Target, meta Meta, create bool,
	run func(*aggregates.VehicleInventoryAggregate, domain.Date) error) (*Result, error) {
	loc, err := s.dealerships.Location(ctx, target.DealershipID)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v, err := s.load(ctx, target, create)
		if err != nil {
			return nil, err
		}
		if target.ExpectedVersion != nil && *target.ExpectedVersion != v.Version() {
			return nil, fmt.Errorf("%w: expected %d, current %d", ErrVersionMismatch, *target.ExpectedVersion, v.Version())
		}
		businessDate := domain.BusinessDate(s.clock.Now(), loc)
		if err := run(v, businessDate); err != nil {
			return nil, err
		}
		if !v.IsUnsaved() {
			return &Result{Vehicle: v, BusinessDate: businessDate}, nil
		}
		err = s.store.SaveWithMetadata(ctx, v, meta.toMap())
		if err == nil {
			return &Result{Vehicle: v, BusinessDate: businessDate, Changed: true}, nil
		}
		if !IsRetryable(err) {
			return nil, err
		}
		if attempt >= MaxAttempts {
			return nil, fmt.Errorf("giving up after %d attempts: %w", attempt, err)
		}
		s.log.WarnContext(ctx, "retrying command after retryable save failure",
			"vehicle_id", target.VehicleID, "attempt", attempt, "error", err)
		if err := backoff(ctx, attempt); err != nil {
			return nil, err
		}
	}
}

// backoff waits a short, jittered time before the next attempt, so competing writers
// do not retry in lockstep. It returns early when ctx ends.
func backoff(ctx context.Context, attempt int) error {
	d := time.Duration(attempt)*5*time.Millisecond + rand.N(10*time.Millisecond)
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// load returns the stored vehicle, or a new empty aggregate when create is set and
// the stream does not exist. A vehicle of another dealership is reported as not found.
func (s *Service) load(ctx context.Context, target Target, create bool) (*aggregates.VehicleInventoryAggregate, error) {
	v := &aggregates.VehicleInventoryAggregate{}
	err := s.store.Get(ctx, target.VehicleID, v)
	switch {
	case errors.Is(err, ErrStreamNotFound) && create:
		v = &aggregates.VehicleInventoryAggregate{}
		if err := v.SetID(target.VehicleID); err != nil {
			return nil, err
		}
		return v, nil
	case errors.Is(err, ErrStreamNotFound):
		return nil, fmt.Errorf("%w: %s", ErrVehicleNotFound, target.VehicleID)
	case err != nil:
		return nil, err
	}
	if v.DealershipID() != target.DealershipID {
		if create {
			// The ID is taken; do not reveal by whom.
			return nil, fmt.Errorf("%w: vehicle %s", domain.ErrVehicleAlreadyStocked, target.VehicleID)
		}
		return nil, fmt.Errorf("%w: %s", ErrVehicleNotFound, target.VehicleID)
	}
	return v, nil
}
