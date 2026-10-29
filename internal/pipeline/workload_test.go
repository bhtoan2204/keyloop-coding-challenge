//go:build integration && pipeline

package pipeline_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/commandstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/referencedata"
)

// fixedClock pins the business date: stocked on 2026-06-01, a vehicle is aging.
type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) }

// workload drives vehicles through their lifecycle with the real command service:
// stock, propose, revise, cancel, propose again, sell (6 events per vehicle).
type workload struct {
	commands *command.Service
	mu       sync.Mutex
	steps    map[string]int
	actions  map[string]string
	issued   int
}

const lifecycleSteps = 6

func newWorkload(e *env) *workload {
	store := eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(e.mysql, e.serializer), 0)
	return &workload{
		commands: command.NewService(
			commandstore.New(store),
			referencedata.NewDealerships(e.mysql),
			fixedClock{},
			testLog(),
		),
		steps:   make(map[string]int),
		actions: make(map[string]string),
	}
}

// next issues the vehicle's next command. It returns false once the vehicle is sold.
func (w *workload) next(ctx context.Context, vehicleID string) (bool, error) {
	w.mu.Lock()
	step, actionID := w.steps[vehicleID], w.actions[vehicleID]
	w.mu.Unlock()
	if step >= lifecycleSteps {
		return false, nil
	}
	meta := command.Meta{
		ActorID:       "pipeline-test",
		CorrelationID: fmt.Sprintf("%s-%d", vehicleID, step),
	}
	target := command.Target{
		DealershipID: "d-001",
		VehicleID:    vehicleID,
	}
	var (
		res *command.Result
		err error
	)
	switch step {
	case 0:
		res, err = w.commands.StockVehicle(ctx, command.StockVehicle{
			DealershipID: "d-001",
			VehicleID:    vehicleID,
			VIN:          "1HGCM82633A004352",
			Make:         "Toyota",
			Model:        "Corolla",
			ModelYear:    2023,
			StockedOn:    domain.NewDate(2026, 6, 1),
		}, meta)
	case 1, 4:
		res, err = w.commands.ProposeAction(ctx, command.ProposeAction{
			Target:     target,
			ActionType: domain.ActionHold,
			Note:       fmt.Sprintf("step %d", step),
		}, meta)
	case 2:
		res, err = w.commands.ReviseAction(ctx, command.ReviseAction{
			Target:     target,
			ActionID:   actionID,
			ActionType: domain.ActionAuction,
			Note:       "lot 7",
		}, meta)
	case 3:
		res, err = w.commands.CancelAction(ctx, command.CancelAction{
			Target:   target,
			ActionID: actionID,
			Reason:   "buyer found",
		}, meta)
	case 5:
		res, err = w.commands.SellVehicle(ctx, command.SellVehicle{
			Target: target,
			SoldOn: domain.NewDate(2026, 10, 29),
		}, meta)
	}
	if err != nil {
		return false, fmt.Errorf("%s step %d: %w", vehicleID, step, err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.steps[vehicleID] = step + 1
	w.issued++
	if res.ActionID != "" {
		w.actions[vehicleID] = res.ActionID
	}
	return true, nil
}

// advance issues up to steps commands per vehicle, round robin across vehicles (so
// events of different vehicles interleave in the binlog).
func (w *workload) advance(t *testing.T, vehicles []string, steps int) {
	t.Helper()
	for i := 0; i < steps; i++ {
		for _, v := range vehicles {
			if _, err := w.next(ctx, v); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// background issues commands for fresh vehicles (prefix-0, prefix-1, …) until stop is
// called; stop returns the first error and the number of commands issued.
func (w *workload) background(prefix string, vehicles int) (stop func() (int, error)) {
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		defer close(errs)
		// Every lifecycleSteps rounds a new generation of vehicles starts.
		for round := 0; ctx.Err() == nil; round++ {
			for i := 0; i < vehicles && ctx.Err() == nil; i++ {
				if _, err := w.next(ctx, fmt.Sprintf("%s-%d-%d", prefix, round/lifecycleSteps, i)); err != nil {
					if ctx.Err() != nil {
						return // cancelled mid-command
					}
					errs <- err
					return
				}
			}
		}
	}()
	return func() (int, error) {
		cancel()
		err := <-errs
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.issued, err
	}
}

func (w *workload) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.issued
}

func vehicleIDs(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s-%d", prefix, i)
	}
	return out
}
