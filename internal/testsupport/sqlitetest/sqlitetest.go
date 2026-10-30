// Package sqlitetest opens the in-memory SQLite databases the fast unit tests run the
// event store on. The runtime engine is PostgreSQL with schema-qualified SQL
// (event_store.es_event, …); attaching a second in-memory database named event_store
// lets the same SQL text run on SQLite.
package sqlitetest

import (
	"net/url"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open returns an empty database with event_store attached, private to the test. It
// has a single connection: an ATTACH applies to one connection only.
func Open(t testing.TB) *gorm.DB {
	t.Helper()
	// Escaped: the name is part of a file: URI (a '?' or '%' in a subtest name would
	// otherwise corrupt it).
	name := url.PathEscape(strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`ATTACH DATABASE 'file:` + name + `_event_store?mode=memory&cache=shared' AS event_store`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}
