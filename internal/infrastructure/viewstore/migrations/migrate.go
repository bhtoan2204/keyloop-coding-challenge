// Package migrations creates and versions the read_model schema.
package migrations

import (
	"fmt"

	"gorm.io/gorm"
)

// Advisory-lock class of read-model migrations (the runtime locks are in repositories).
const lockClassMigrate = 52001

type migration struct {
	version int
	name    string
	apply   func(tx *gorm.DB) error
}

var migrations = []migration{
	{
		version: 1,
		name:    "read model tables",
		apply:   createReadModelTables,
	},
}

// Migrate applies the read_model schema. It needs the event_store schema first (the
// dealership foreign key points there). PostgreSQL DDL is transactional, so each
// version is applied and recorded atomically; concurrent callers are serialised with a
// transaction-scoped advisory lock.
func Migrate(db *gorm.DB) error {
	// The bootstrap DDL runs under the migration lock: CREATE ... IF NOT EXISTS is not
	// safe against a concurrent creator in PostgreSQL.
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(?, 0)`, lockClassMigrate).Error; err != nil {
			return err
		}
		return tx.Exec(`CREATE SCHEMA IF NOT EXISTS read_model;
CREATE TABLE IF NOT EXISTS read_model.schema_migrations (
	version    INT PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`).Error
	})
	if err != nil {
		return fmt.Errorf("read model migrate: %w", err)
	}
	latest := migrations[len(migrations)-1].version
	for _, m := range migrations {
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(`SELECT pg_advisory_xact_lock(?, 0)`, lockClassMigrate).Error; err != nil {
				return err
			}
			var newest int
			if err := tx.Raw(`SELECT COALESCE(max(version), 0) FROM read_model.schema_migrations`).Scan(&newest).Error; err != nil {
				return err
			}
			if newest > latest {
				return fmt.Errorf("database has read-model schema version %d, this build knows up to %d", newest, latest)
			}
			if newest >= m.version {
				return nil
			}
			if err := m.apply(tx); err != nil {
				return err
			}
			return tx.Exec(`INSERT INTO read_model.schema_migrations(version, name) VALUES (?, ?)`, m.version, m.name).Error
		})
		if err != nil {
			return fmt.Errorf("read model migrate: version %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

// createReadModelTables creates the projected tables. It needs event_store.dealership
// (the foreign key target) and refuses a database that still has read-model tables in
// public from an earlier layout: this build supports fresh installations only and never
// moves, overwrites or drops existing data.
func createReadModelTables(tx *gorm.DB) error {
	var hasDealerships bool
	if err := tx.Raw(`SELECT to_regclass('event_store.dealership') IS NOT NULL`).Scan(&hasDealerships).Error; err != nil {
		return err
	}
	if !hasDealerships {
		return fmt.Errorf("event_store.dealership is missing: migrate the event store first")
	}
	var earlier []string
	err := tx.Raw(`SELECT name FROM unnest(ARRAY['inventory_vehicle', 'vehicle_action_history', 'projection_aggregate', 'projection_run']) AS name
		WHERE to_regclass('public.' || name) IS NOT NULL ORDER BY name`).Scan(&earlier).Error
	if err != nil {
		return err
	}
	if len(earlier) > 0 {
		return fmt.Errorf("public has read-model tables of an earlier layout %v: unsupported, start from an empty database or remove them by hand", earlier)
	}
	if err := tx.Exec(readModelTablesDDL).Error; err != nil {
		return err
	}
	return tx.Exec(dealershipForeignKeyDDL).Error
}
