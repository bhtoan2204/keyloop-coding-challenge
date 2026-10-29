//go:build integration

package eventstore_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/package/eventsourcing"
)

// lockedAggregate saves aggregate "inv" at version 1 and returns a store whose
// connections give up on row locks after 1 second, plus an admin transaction that
// holds the exclusive lock on the aggregate's es_aggregate row until released.
func lockedAggregate(t *testing.T) (eventstore.AggregateStore, *sql.Tx) {
	t.Helper()
	admin, cfg := newMySQLDatabase(t)
	if err := repos.Migrate(admin); err != nil {
		t.Fatal(err)
	}
	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&qtyAggregate{}); err != nil {
		t.Fatal(err)
	}
	adminStore := eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(admin, serializer), 0)
	agg := newQty(t, "inv")
	addQty(t, agg, 1)
	mustSave(t, adminStore, agg)

	shortWait := cfg.Clone()
	shortWait.Params = map[string]string{"innodb_lock_wait_timeout": "1"}
	store := eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(openGorm(t, shortWait), serializer), 0)

	sqlDB, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	holder, err := sqlDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })
	if _, err := holder.Exec(`SELECT version FROM es_aggregate WHERE id = 'inv' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	return store, holder
}

func TestMySQLLockWaitTimeoutIsRetryableAndSaveCanBeRetried(t *testing.T) {
	store, holder := lockedAggregate(t)
	agg := mustLoad(t, store, "inv")
	addQty(t, agg, 1)

	err := store.Save(context.Background(), agg)
	if !errors.Is(err, eventstore.ErrRetryable) || errors.Is(err, eventstore.ErrConcurrencyConflict) || !eventstore.IsRetryable(err) {
		t.Fatalf("want ErrRetryable (lock wait timeout), got %v", err)
	}
	if !agg.IsUnsaved() || agg.BaseVersion() != 1 {
		t.Fatalf("failed save changed the aggregate: unsaved=%v base=%d", agg.IsUnsaved(), agg.BaseVersion())
	}

	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	// Nothing was persisted by the failed attempt, so the same pending events still
	// apply to the stored version.
	mustSave(t, store, agg)
	if loaded := mustLoad(t, store, "inv"); loaded.qty != 2 || loaded.Version() != 2 {
		t.Fatalf("after retry qty=%d version=%d", loaded.qty, loaded.Version())
	}
}

func TestMySQLContextErrorsAreNotRetryable(t *testing.T) {
	t.Run("cancelled before save", func(t *testing.T) {
		_, _, store := newStore(t, 0)
		agg := newQty(t, "inv")
		addQty(t, agg, 1)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := store.Save(ctx, agg)
		if !errors.Is(err, context.Canceled) || eventstore.IsRetryable(err) {
			t.Fatalf("got %v", err)
		}
		if !agg.IsUnsaved() {
			t.Fatal("pending events cleared")
		}
	})

	t.Run("deadline while waiting for a lock", func(t *testing.T) {
		store, _ := lockedAggregate(t)
		agg := mustLoad(t, store, "inv")
		addQty(t, agg, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		err := store.Save(ctx, agg)
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, eventstore.ErrConcurrencyConflict) {
			t.Fatalf("got %v", err)
		}
		if !agg.IsUnsaved() {
			t.Fatal("pending events cleared")
		}
	})
}
