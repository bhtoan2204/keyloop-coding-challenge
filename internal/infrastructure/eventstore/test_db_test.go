package eventstore_test

import (
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/testsupport/sqlitetest"
	"keyloop-coding-challenge/pkg/eventsourcing"
)

// openDB returns a migrated database for one test and openRawDB an empty one. The
// default is in-memory SQLite; building with -tags integration switches both to a
// throwaway PostgreSQL database (postgres_db_integration_test.go), so the same tests
// verify the runtime engine.
var (
	openDB    = openSQLiteDB
	openRawDB = sqlitetest.Open
)

func openSQLiteDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := sqlitetest.Open(t)
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func newStoreWith(t *testing.T, db *gorm.DB, serializer eventsourcing.Serializer, interval int, opts ...eventstore.Option) eventstore.AggregateStore {
	t.Helper()
	return eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(db, serializer), interval, opts...)
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

func mustExec(t *testing.T, db *gorm.DB, stmt string, args ...interface{}) {
	t.Helper()
	if err := db.Exec(stmt, args...).Error; err != nil {
		t.Fatalf("%s: %v", strings.TrimSpace(stmt), err)
	}
}

func appliedVersions(t *testing.T, db *gorm.DB) []int {
	t.Helper()
	var versions []int
	if err := db.Raw(`SELECT version FROM event_store.schema_migrations ORDER BY version`).Scan(&versions).Error; err != nil {
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
	if err := db.Raw(`SELECT version, aggregate_type, schema_version FROM event_store.es_event WHERE aggregate_id = ? ORDER BY version`, id).
		Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func tableExists(t *testing.T, db *gorm.DB, table string) bool {
	t.Helper()
	query := `SELECT count(*) FROM event_store.sqlite_master WHERE type = 'table' AND name = ?`
	args := []interface{}{table}
	if db.Dialector.Name() == "postgres" {
		query = `SELECT count(*) FROM pg_tables WHERE schemaname = 'event_store' AND tablename = ?`
	}
	var n int
	if err := db.Raw(query, args...).Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// retryable mirrors command.IsRetryable over the store's errors: reload and re-run.
func retryable(err error) bool {
	return errors.Is(err, eventstore.ErrConcurrencyConflict) || errors.Is(err, eventstore.ErrRetryable)
}
