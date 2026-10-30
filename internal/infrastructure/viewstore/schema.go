package viewstore

import (
	"fmt"

	"gorm.io/gorm"
)

// Advisory-lock class IDs used by the read model (two-int form avoids collisions).
const (
	lockClassMigrate    = 52001
	lockClassAggregate  = 52002
	lockClassProjection = 52003
)

type migration struct {
	version int
	name    string
	apply   func(tx *gorm.DB) error
}

var migrations = []migration{
	{
		version: 1,
		name:    "read model tables",
		apply:   createOrAdoptReadModelTables,
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
