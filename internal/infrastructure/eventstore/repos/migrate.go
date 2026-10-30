package repos

import (
	"fmt"

	"gorm.io/gorm"
)

// lockClassMigrate is the advisory-lock class serialising event-store migrations
// (two-int form; the read model uses its own classes).
const lockClassMigrate = 51001

// migration is one schema version. Each version is applied and recorded in one
// transaction (PostgreSQL and SQLite DDL are transactional), so a failed version
// leaves nothing behind and is simply retried by the next run.
type migration struct {
	version  int
	name     string
	postgres string
	sqlite   string
}

// Migrate brings the event_store schema up to date. Concurrent callers are serialised
// with a transaction-scoped advisory lock; a database newer than this build is refused.
func Migrate(db *gorm.DB) error {
	// CREATE ... IF NOT EXISTS is not safe against a concurrent creator in PostgreSQL
	// (both see "missing", one fails on the catalog's unique index), so the bootstrap
	// DDL runs under the migration lock too.
	err := db.Transaction(func(tx *gorm.DB) error {
		if isPostgres(tx) {
			if err := tx.Exec(`SELECT pg_advisory_xact_lock(?, 0)`, lockClassMigrate).Error; err != nil {
				return err
			}
		}
		return tx.Exec(schemaMigrationsDDL(tx)).Error
	})
	if err != nil {
		return fmt.Errorf("event store migrate: %w", err)
	}
	latest := migrations[len(migrations)-1].version
	for _, m := range migrations {
		err := db.Transaction(func(tx *gorm.DB) error {
			if isPostgres(tx) {
				if err := tx.Exec(`SELECT pg_advisory_xact_lock(?, 0)`, lockClassMigrate).Error; err != nil {
					return err
				}
			}
			var newest int
			if err := tx.Raw(`SELECT COALESCE(max(version), 0) FROM event_store.schema_migrations`).Scan(&newest).Error; err != nil {
				return err
			}
			if newest > latest {
				// An older release must not run against a schema it does not know.
				return fmt.Errorf("database has event-store schema version %d, this build knows up to %d", newest, latest)
			}
			if newest >= m.version {
				return nil
			}
			ddl := m.sqlite
			if isPostgres(tx) {
				ddl = m.postgres
			}
			if err := tx.Exec(ddl).Error; err != nil {
				return err
			}
			return tx.Exec(`INSERT INTO event_store.schema_migrations(version, name) VALUES (?, ?)`, m.version, m.name).Error
		})
		if err != nil {
			return fmt.Errorf("event store migrate: version %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

func schemaMigrationsDDL(db *gorm.DB) string {
	if isPostgres(db) {
		return `CREATE SCHEMA IF NOT EXISTS event_store;
CREATE TABLE IF NOT EXISTS event_store.schema_migrations (
	version    INT PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`
	}
	// SQLite (tests): the caller attaches a database named event_store.
	return `CREATE TABLE IF NOT EXISTS event_store.schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`
}

func isPostgres(db *gorm.DB) bool {
	return db.Dialector.Name() == "postgres"
}
