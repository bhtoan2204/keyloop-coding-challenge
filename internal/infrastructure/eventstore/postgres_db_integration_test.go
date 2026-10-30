//go:build integration

package eventstore_test

import (
	"testing"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/testsupport/pgtest"
)

// With -tags integration every AggregateStore test runs against PostgreSQL, each in a
// throwaway database (PG_TEST_DSN; a missing DSN fails the test, it never skips).
func init() {
	openDB = openPostgresDB
	openRawDB = openRawPostgresDB
}

func openRawPostgresDB(t testing.TB) *gorm.DB {
	t.Helper()
	db, _ := pgtest.NewDatabase(t.(*testing.T))
	return db
}

func openPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, _ := pgtest.NewDatabase(t)
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}
