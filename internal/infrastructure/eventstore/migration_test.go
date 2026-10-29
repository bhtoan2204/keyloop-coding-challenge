package eventstore_test

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openRawDB returns an empty, unmigrated database (SQLite by default, MySQL with
// -tags integration).
var openRawDB = openRawSQLiteDB

func openRawSQLiteDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := "raw_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// The schema as created by the original, unversioned Migrate (commit 7d04502). Frozen
// here on purpose: the upgrade test must start from what real databases contain.
const legacyMySQLSchema = `
CREATE TABLE IF NOT EXISTS es_aggregate (
    id VARCHAR(255) NOT NULL PRIMARY KEY,
    version BIGINT NOT NULL,
    aggregate_type VARCHAR(255) NOT NULL
);
CREATE TABLE IF NOT EXISTS es_event (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    aggregate_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(255) NOT NULL,
    version BIGINT NOT NULL,
    data TEXT NOT NULL,
    metadata TEXT NULL,
    created_at BIGINT NOT NULL,
    UNIQUE KEY uq_es_event_aggregate_version (aggregate_id, version)
);
CREATE TABLE IF NOT EXISTS es_aggregate_snapshot (
    aggregate_id VARCHAR(255) NOT NULL,
    version BIGINT NOT NULL,
    data TEXT NOT NULL,
    PRIMARY KEY (aggregate_id, version)
)`

const legacySQLiteSchema = `
CREATE TABLE IF NOT EXISTS es_aggregate (
	id TEXT NOT NULL PRIMARY KEY,
	version INTEGER NOT NULL,
	aggregate_type TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS es_event (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	aggregate_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	version INTEGER NOT NULL,
	data TEXT NOT NULL,
	metadata TEXT,
	created_at INTEGER NOT NULL,
	UNIQUE (aggregate_id, version)
);
CREATE TABLE IF NOT EXISTS es_aggregate_snapshot (
	aggregate_id TEXT NOT NULL,
	version INTEGER NOT NULL,
	data TEXT NOT NULL,
	PRIMARY KEY (aggregate_id, version)
)`

// createLegacyDatabase builds the original schema and stores aggregate "legacy" with
// two events using the original INSERT statements (no aggregate_type/schema_version).
func createLegacyDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db := openRawDB(t)
	ddl := legacySQLiteSchema
	if db.Dialector.Name() == "mysql" {
		ddl = legacyMySQLSchema
	}
	for _, stmt := range strings.Split(ddl, ";") {
		mustExec(t, db, stmt)
	}
	mustExec(t, db, `INSERT INTO es_aggregate(id, version, aggregate_type) VALUES(?, 2, ?)`, "legacy", "qtyAggregate")
	for v := 1; v <= 2; v++ {
		mustExec(t, db, `INSERT INTO es_event(aggregate_id, event_type, version, data, metadata, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
			"legacy", "qtyEvent", v, `{"delta":5}`, `{"source":"v1"}`, 1700000000+v)
	}
	return db
}

func mustExec(t *testing.T, db *gorm.DB, stmt string, args ...interface{}) {
	t.Helper()
	if err := db.Exec(stmt, args...).Error; err != nil {
		t.Fatalf("%s: %v", strings.TrimSpace(stmt), err)
	}
}

func appliedVersions(t *testing.T, db *gorm.DB) []int {
	t.Helper()
	var versions []int
	if err := db.Raw(`SELECT version FROM es_schema_migrations ORDER BY version`).Scan(&versions).Error; err != nil {
		t.Fatal(err)
	}
	return versions
}

type streamColumns struct {
	Version       int
	AggregateType string
	SchemaVersion int
}

func eventStreamColumns(t *testing.T, db *gorm.DB, id string) []streamColumns {
	t.Helper()
	var rows []streamColumns
	if err := db.Raw(`SELECT version, aggregate_type, schema_version FROM es_event WHERE aggregate_id = ? ORDER BY version`, id).
		Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func newStoreWith(t *testing.T, db *gorm.DB, serializer eventsourcing.Serializer, interval int) eventstore.AggregateStore {
	t.Helper()
	return eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(db, serializer), interval)
}

// newStoreFor builds a qtyAggregate store on an already migrated database.
func newStoreFor(t *testing.T, db *gorm.DB, interval int) (*gorm.DB, eventsourcing.Serializer, eventstore.AggregateStore) {
	t.Helper()
	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&qtyAggregate{}); err != nil {
		t.Fatal(err)
	}
	return db, serializer, newStoreWith(t, db, serializer, interval)
}

func TestMigrateFreshDatabase(t *testing.T) {
	db := openRawDB(t)
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := repos.Migrate(db); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := appliedVersions(t, db); !reflect.DeepEqual(got, []int{1, 2, 3}) {
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
}

func TestMigrateUpgradesOriginalSchemaWithExistingEvents(t *testing.T) {
	db := createLegacyDatabase(t)
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if got := appliedVersions(t, db); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("applied versions %v", got)
	}
	want := []streamColumns{{1, "qtyAggregate", 1}, {2, "qtyAggregate", 1}}
	if got := eventStreamColumns(t, db, "legacy"); !reflect.DeepEqual(got, want) {
		t.Fatalf("backfilled columns %+v, want %+v", got, want)
	}

	// Existing events stay readable through the unchanged AggregateStore, including
	// their original metadata and timestamps, and the stream continues contiguously.
	_, _, store := newStoreFor(t, db, 0)
	loaded := mustLoad(t, store, "legacy")
	if loaded.qty != 10 || loaded.Version() != 2 {
		t.Fatalf("legacy qty=%d version=%d", loaded.qty, loaded.Version())
	}
	if meta, ok := loaded.applied[0].meta.(map[string]interface{}); !ok || meta["source"] != "v1" || loaded.applied[0].at != 1700000001 {
		t.Fatalf("legacy event details lost: %+v", loaded.applied[0])
	}
	addQty(t, loaded, 1)
	mustSave(t, store, loaded)
	want = append(want, streamColumns{3, "qtyAggregate", 1})
	if got := eventStreamColumns(t, db, "legacy"); !reflect.DeepEqual(got, want) {
		t.Fatalf("after append %+v", got)
	}
}

func TestMigrateResumesAfterPartialRun(t *testing.T) {
	// Simulates a migration that stopped after its first ALTER TABLE: MySQL DDL is not
	// transactional, so the column exists but version 2 was never recorded.
	db := createLegacyDatabase(t)
	colType := "TEXT NOT NULL DEFAULT ''"
	if db.Dialector.Name() == "mysql" {
		colType = "VARCHAR(255) NOT NULL DEFAULT ''"
	}
	mustExec(t, db, "ALTER TABLE es_event ADD COLUMN aggregate_type "+colType)
	if err := repos.Migrate(db); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := appliedVersions(t, db); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("applied versions %v", got)
	}
	want := []streamColumns{{1, "qtyAggregate", 1}, {2, "qtyAggregate", 1}}
	if got := eventStreamColumns(t, db, "legacy"); !reflect.DeepEqual(got, want) {
		t.Fatalf("columns %+v", got)
	}
}

func TestMigrateConcurrentCallers(t *testing.T) {
	db := openRawDB(t)
	if db.Dialector.Name() != "mysql" {
		// In-memory SQLite has one writer; the named-lock path is MySQL only.
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
	if got := appliedVersions(t, db); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("applied versions %v", got)
	}
}

func TestSaveRollsBackWhenALaterEventFails(t *testing.T) {
	db, serializer, _ := newStore(t, 0)
	if err := serializer.RegisterAggregate(&brittleAggregate{}); err != nil {
		t.Fatal(err)
	}
	store := newStoreWith(t, db, serializer, 0)

	agg := &brittleAggregate{}
	if err := agg.SetID("brittle"); err != nil {
		t.Fatal(err)
	}
	if err := agg.ApplyChange(agg, &qtyEvent{Delta: 1}); err != nil {
		t.Fatal(err)
	}
	// The second event cannot be serialised, so its INSERT never happens after the
	// first event's INSERT already ran inside the transaction.
	if err := agg.ApplyChange(agg, &unencodableEvent{C: make(chan int)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), agg); err == nil {
		t.Fatal("expected save to fail")
	}
	if v := eventVersions(t, db, "brittle"); len(v) != 0 {
		t.Fatalf("partial events persisted: %v", v)
	}
	var aggregates int64
	if err := db.Raw(`SELECT COUNT(*) FROM es_aggregate WHERE id = ?`, "brittle").Scan(&aggregates).Error; err != nil {
		t.Fatal(err)
	}
	if aggregates != 0 {
		t.Fatal("es_aggregate row persisted")
	}
	if len(agg.Events()) != 2 || agg.BaseVersion() != 0 {
		t.Fatalf("pending=%d base=%d", len(agg.Events()), agg.BaseVersion())
	}
}

// brittleAggregate accepts an event that cannot be JSON-encoded, to inject a failure
// in the middle of a multi-event append.
type brittleAggregate struct {
	eventsourcing.AggregateRoot
}

type unencodableEvent struct {
	C chan int
}

func (a *brittleAggregate) RegisterEvents(register eventsourcing.RegisterEventsFunc) error {
	return register(&qtyEvent{}, &unencodableEvent{})
}

func (a *brittleAggregate) Transition(eventsourcing.Event) error { return nil }
