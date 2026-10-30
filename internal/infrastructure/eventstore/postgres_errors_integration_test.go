//go:build integration

package eventstore_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/testsupport/pgtest"
	"keyloop-coding-challenge/pkg/eventsourcing"
)

// lockedAggregate stores aggregate "inv" at version 1 and returns a store whose
// connections give up waiting for a lock after 1 s (lock_timeout), plus an open
// transaction that holds the aggregate row lock.
func lockedAggregate(t *testing.T) (eventstore.AggregateStore, *sql.Tx) {
	t.Helper()
	admin, dsn := pgtest.NewDatabase(t)
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

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("lock_timeout", "1000")
	u.RawQuery = q.Encode()
	store := eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(pgtest.Open(t, u.String()), serializer), 0)

	sqlDB, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	holder, err := sqlDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })
	if _, err := holder.Exec(`SELECT version FROM event_store.es_aggregate WHERE id = 'inv' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	return store, holder
}

// TestPostgresLockTimeoutIsRetryableAndSaveCanBeRetried: SQLSTATE 55P03 rolls the
// transaction back, so it is retryable (not a concurrency conflict) and the same
// pending events still apply once the lock is gone.
func TestPostgresLockTimeoutIsRetryableAndSaveCanBeRetried(t *testing.T) {
	store, holder := lockedAggregate(t)
	agg := mustLoad(t, store, "inv")
	addQty(t, agg, 1)

	err := store.Save(context.Background(), agg)
	if !errors.Is(err, eventstore.ErrRetryable) || errors.Is(err, eventstore.ErrConcurrencyConflict) || !retryable(err) {
		t.Fatalf("want ErrRetryable (lock not available), got %v", err)
	}
	if !agg.IsUnsaved() || agg.BaseVersion() != 1 {
		t.Fatalf("failed save changed the aggregate: unsaved=%v base=%d", agg.IsUnsaved(), agg.BaseVersion())
	}

	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	mustSave(t, store, agg)
	if loaded := mustLoad(t, store, "inv"); loaded.qty != 2 || loaded.Version() != 2 {
		t.Fatalf("after retry qty=%d version=%d", loaded.qty, loaded.Version())
	}
}

func TestPostgresContextErrorsAreNotRetryable(t *testing.T) {
	t.Run("cancelled before save", func(t *testing.T) {
		_, _, store := newStore(t, 0)
		agg := newQty(t, "inv")
		addQty(t, agg, 1)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := store.Save(ctx, agg)
		if !errors.Is(err, context.Canceled) || retryable(err) {
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
		if retryable(err) || err == nil {
			t.Fatalf("got %v", err)
		}
		if !errors.Is(err, context.DeadlineExceeded) && !repos.IsUnavailable(err) {
			t.Fatalf("deadline not reported as a deadline or an unavailable database: %v", err)
		}
		if !agg.IsUnsaved() {
			t.Fatal("pending events cleared")
		}
	})
}
