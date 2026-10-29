package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/aggregates"

	"github.com/google/uuid"
)

// MaxAttempts bounds how often a command is re-run after a retryable save failure.
const MaxAttempts = 3

// IsRetryable reports a save failure after which reloading and re-running the
// command may succeed: an optimistic conflict or a rolled-back transient failure.
func IsRetryable(err error) bool {
	return errors.Is(err, ErrConflict) || errors.Is(err, ErrTransient)
}

type Service struct {
	store       VehicleStore
	dealerships DealershipDirectory
	clock       Clock
	newID       func() string
	log         *slog.Logger
}

func NewService(store VehicleStore, dealerships DealershipDirectory, clock Clock, log *slog.Logger) *Service {
	return &Service{store: store, dealerships: dealerships, clock: clock, newID: newUUIDv7, log: log}
}

func newUUIDv7() string {
	return uuid.Must(uuid.NewV7()).String()
}

// StockVehicle creates a vehicle's stream. The dealership comes from the caller's
// route, never from the request body.
func (s *Service) StockVehicle(ctx context.Context, cmd StockVehicle, meta Meta) (*Result, error) {
	vehicleID := cmd.VehicleID
	if vehicleID == "" {
		vehicleID = s.newID()
	}
	stock := aggregates.StockVehicle{
		DealershipID: cmd.DealershipID, VIN: cmd.VIN, Make: cmd.Make, Model: cmd.Model,
		ModelYear: cmd.ModelYear, StockedOn: cmd.StockedOn,
	}
	return s.execute(ctx, Target{DealershipID: cmd.DealershipID, VehicleID: vehicleID}, meta, true,
		func(v *aggregates.VehicleInventoryAggregate, bd domain.Date) error {
			return v.Stock(stock, bd)
		})
}

func (s *Service) ProposeAction(ctx context.Context, cmd ProposeAction, meta Meta) (*Result, error) {
	actionID := s.newID()
	res, err := s.execute(ctx, cmd.Target, meta, false, func(v *aggregates.VehicleInventoryAggregate, bd domain.Date) error {
		return v.ProposeAction(aggregates.ProposeAction{ActionID: actionID, ActionType: cmd.ActionType, Note: cmd.Note}, bd)
	})
	if err != nil {
		return nil, err
	}
	res.ActionID = actionID
	return res, nil
}

func (s *Service) ReviseAction(ctx context.Context, cmd ReviseAction, meta Meta) (*Result, error) {
	return s.execute(ctx, cmd.Target, meta, false, func(v *aggregates.VehicleInventoryAggregate, _ domain.Date) error {
		return v.ReviseAction(aggregates.ReviseAction{ActionID: cmd.ActionID, ActionType: cmd.ActionType, Note: cmd.Note})
	})
}

func (s *Service) CancelAction(ctx context.Context, cmd CancelAction, meta Meta) (*Result, error) {
	return s.execute(ctx, cmd.Target, meta, false, func(v *aggregates.VehicleInventoryAggregate, _ domain.Date) error {
		return v.CancelAction(aggregates.CancelAction{ActionID: cmd.ActionID, Reason: cmd.Reason})
	})
}

func (s *Service) SellVehicle(ctx context.Context, cmd SellVehicle, meta Meta) (*Result, error) {
	return s.execute(ctx, cmd.Target, meta, false, func(v *aggregates.VehicleInventoryAggregate, bd domain.Date) error {
		return v.Sell(aggregates.SellVehicle{SoldOn: cmd.SoldOn}, bd)
	})
}

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
