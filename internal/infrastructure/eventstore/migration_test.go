package eventstore_test

import (
	"reflect"
	"sync"
	"testing"

	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
)

func TestMigrateFreshDatabase(t *testing.T) {
	db := openRawDB(t)
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := repos.Migrate(db); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := appliedVersions(t, db); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("applied versions %v", got)
	}
	_, _, store := newStoreFor(t, db, 0)
	agg := newQty(t, "fresh")
	addQty(t, agg, 1)
	addQty(t, agg, 2)
	mustSave(t, store, agg)

	want := []streamColumns{{1, "qtyAggregate", 1}, {2, "qtyAggregate", 1}}
	if got := eventStreamColumns(t, db, "fresh"); !reflect.DeepEqual(got, want) {
		t.Fatalf("stream columns %+v, want %+v", got, want)
	}
	var zones int
	if err := db.Raw(`SELECT count(*) FROM event_store.dealership WHERE dealership_id IN ('d-001', 'd-002')`).Scan(&zones).Error; err != nil || zones != 2 {
		t.Fatalf("seeded dealerships: %d (%v)", zones, err)
	}
}

// TestMigrateFailedVersionLeavesNothingBehind: each version runs in one transaction,
// so a version that fails half-way records nothing and leaves no table behind; the
// next run, once the obstacle is gone, applies it completely.
func TestMigrateFailedVersionLeavesNothingBehind(t *testing.T) {
	db := openRawDB(t)
	if db.Dialector.Name() == "postgres" {
		mustExec(t, db, `CREATE SCHEMA event_store`)
	}
	// Version 1 creates es_aggregate, es_event, then es_aggregate_snapshot: the last
	// CREATE TABLE fails.
	mustExec(t, db, `CREATE TABLE event_store.es_aggregate_snapshot (x INTEGER)`)
	if err := repos.Migrate(db); err == nil {
		t.Fatal("migration succeeded over a conflicting table")
	}
	if tableExists(t, db, "es_aggregate") || tableExists(t, db, "es_event") {
		t.Fatal("a failed version left tables behind")
	}
	if got := appliedVersions(t, db); len(got) != 0 {
		t.Fatalf("a failed version was recorded: %v", got)
	}

	mustExec(t, db, `DROP TABLE event_store.es_aggregate_snapshot`)
	if err := repos.Migrate(db); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := appliedVersions(t, db); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("applied versions after retry %v", got)
	}
}

func TestMigrateConcurrentCallers(t *testing.T) {
	db := openRawDB(t)
	if db.Dialector.Name() != "postgres" {
		t.Log("in-memory SQLite has one connection; the advisory-lock path runs with -tags integration")
		if err := repos.Migrate(db); err != nil {
			t.Fatal(err)
		}
		return
	}
	errs := make([]error, 4)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = repos.Migrate(db)
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("migrator %d: %v", i, err)
		}
	}
	if got := appliedVersions(t, db); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("applied versions %v", got)
	}
}
