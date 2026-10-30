//go:build integration

package eventstore_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"keyloop-coding-challenge/internal/infrastructure/eventstore"
)

const writers = 16

// raceSaves saves every aggregate concurrently. All goroutines block on one channel
// so the transactions overlap; there are no sleeps.
func raceSaves(store eventstore.AggregateStore, aggs []*qtyAggregate) []error {
	start := make(chan struct{})
	errs := make([]error, len(aggs))
	var wg sync.WaitGroup
	for i, agg := range aggs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = store.Save(context.Background(), agg)
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

// outcome counts successes and optimistic-concurrency conflicts; anything else is
// returned so the test can show the unexpected error.
func outcome(errs []error) (ok, conflicts int, other []error) {
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, eventstore.ErrConcurrencyConflict):
			conflicts++
		default:
			other = append(other, err)
		}
	}
	return ok, conflicts, other
}

func TestPostgresConcurrentWritersSameExpectedVersion(t *testing.T) {
	db, _, store := newStore(t, 0)
	for round := 0; round < 10; round++ {
		id := fmt.Sprintf("inv-%d", round)
		first := newQty(t, id)
		addQty(t, first, 1)
		mustSave(t, store, first)

		// Every writer loads version 1 before any of them saves.
		aggs := make([]*qtyAggregate, writers)
		for i := range aggs {
			aggs[i] = mustLoad(t, store, id)
			addQty(t, aggs[i], 1)
		}
		ok, conflicts, other := outcome(raceSaves(store, aggs))
		if ok != 1 || conflicts != writers-1 || len(other) > 0 {
			t.Fatalf("round %d: ok=%d conflicts=%d other=%d first other: %v", round, ok, conflicts, len(other), firstErr(other))
		}
		if v := eventVersions(t, db, id); !reflect.DeepEqual(v, []int{1, 2}) {
			t.Fatalf("round %d: versions %v", round, v)
		}
		for _, agg := range aggs {
			if failed := agg.IsUnsaved(); failed == (agg.BaseVersion() == 2) {
				t.Fatalf("round %d: pending events not consistent with outcome: unsaved=%v base=%d", round, failed, agg.BaseVersion())
			}
		}
	}
}

func TestPostgresConcurrentCreationOfSameAggregate(t *testing.T) {
	db, _, store := newStore(t, 0)
	for round := 0; round < 10; round++ {
		id := fmt.Sprintf("new-%d", round)
		aggs := make([]*qtyAggregate, writers)
		for i := range aggs {
			aggs[i] = newQty(t, id)
			addQty(t, aggs[i], 1)
			addQty(t, aggs[i], 1)
		}
		ok, conflicts, other := outcome(raceSaves(store, aggs))
		if ok != 1 || conflicts != writers-1 || len(other) > 0 {
			t.Fatalf("round %d: ok=%d conflicts=%d other=%d first other: %v", round, ok, conflicts, len(other), firstErr(other))
		}
		if v := eventVersions(t, db, id); !reflect.DeepEqual(v, []int{1, 2}) {
			t.Fatalf("round %d: versions %v", round, v)
		}
	}
}

func TestPostgresParallelIndependentAggregates(t *testing.T) {
	db, _, store := newStore(t, 0)
	aggs := make([]*qtyAggregate, writers)
	for i := range aggs {
		aggs[i] = newQty(t, fmt.Sprintf("solo-%d", i))
		for j := 0; j < 3; j++ {
			addQty(t, aggs[i], 1)
		}
	}
	for i, err := range raceSaves(store, aggs) {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	for i := range aggs {
		if v := eventVersions(t, db, fmt.Sprintf("solo-%d", i)); !reflect.DeepEqual(v, []int{1, 2, 3}) {
			t.Fatalf("aggregate %d versions %v", i, v)
		}
	}
}

// TestPostgresNoLostUpdates retries like the application layer: on a retryable error,
// reload the latest state and re-run the change. Every writer's event must land.
func TestPostgresNoLostUpdates(t *testing.T) {
	db, _, store := newStore(t, 0)
	first := newQty(t, "counter")
	addQty(t, first, 1)
	mustSave(t, store, first)

	start := make(chan struct{})
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for attempt := 0; attempt < 100; attempt++ {
				agg := &qtyAggregate{}
				if err := store.Get(context.Background(), "counter", agg); err != nil {
					errs[i] = err
					return
				}
				if err := agg.ApplyChange(agg, &qtyEvent{Delta: 1}); err != nil {
					errs[i] = err
					return
				}
				err := store.Save(context.Background(), agg)
				if err == nil {
					return
				}
				if !eventstore.IsRetryable(err) {
					errs[i] = err
					return
				}
			}
			errs[i] = errors.New("gave up after 100 attempts")
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	loaded := mustLoad(t, store, "counter")
	if loaded.qty != writers+1 || loaded.Version() != writers+1 {
		t.Fatalf("qty=%d version=%d, want %d", loaded.qty, loaded.Version(), writers+1)
	}
	want := make([]int, writers+1)
	for i := range want {
		want[i] = i + 1
	}
	if v := eventVersions(t, db, "counter"); !reflect.DeepEqual(v, want) {
		t.Fatalf("versions %v", v)
	}
}

func firstErr(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return errs[0]
}
