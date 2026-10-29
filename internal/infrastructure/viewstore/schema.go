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
	sql     string
}

// There is deliberately no is_aging or days_in_stock column: both change daily and
// are computed at query time from stocked_on and the dealership business date.
var migrations = []migration{
	{1, "read model tables", `
CREATE TABLE dealership (
	dealership_id TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	timezone      TEXT NOT NULL
);
INSERT INTO dealership(dealership_id, name, timezone) VALUES
	('d-001', 'Keyloop Motors London', 'Europe/London'),
	('d-002', 'Keyloop Motors Sydney', 'Australia/Sydney')
ON CONFLICT (dealership_id) DO NOTHING;

CREATE TABLE inventory_vehicle (
	vehicle_id         TEXT PRIMARY KEY,
	dealership_id      TEXT     NOT NULL REFERENCES dealership,
	vin                TEXT     NOT NULL,
	make               TEXT     NOT NULL,
	model              TEXT     NOT NULL,
	model_year         INT      NOT NULL,
	stocked_on         DATE     NOT NULL,
	status             TEXT     NOT NULL CHECK (status IN ('IN_STOCK', 'SOLD')),
	sold_on            DATE,
	action_id          TEXT,
	action_type        TEXT,
	action_note        TEXT,
	action_proposed_at TIMESTAMPTZ,
	action_updated_at  TIMESTAMPTZ,
	action_updated_by  TEXT,
	version            BIGINT      NOT NULL CHECK (version >= 1),
	last_event_id      BIGINT      NOT NULL,
	updated_at         TIMESTAMPTZ NOT NULL,
	CHECK ((status = 'SOLD') = (sold_on IS NOT NULL))
);
-- Default list, age filters, aging-only and both age sorts (backward scan for age_asc).
CREATE INDEX ix_vehicle_dealer_status_stocked ON inventory_vehicle (dealership_id, status, stocked_on, vehicle_id);
-- make / make+model filters, ordered by age within them.
CREATE INDEX ix_vehicle_dealer_make_model ON inventory_vehicle (dealership_id, status, lower(make), lower(model), stocked_on, vehicle_id);

CREATE TABLE vehicle_action_history (
	vehicle_id     TEXT   NOT NULL REFERENCES inventory_vehicle ON DELETE CASCADE,
	version        BIGINT NOT NULL,
	event_id       BIGINT NOT NULL,
	action_id      TEXT   NOT NULL,
	change         TEXT   NOT NULL CHECK (change IN ('PROPOSED', 'REVISED', 'CANCELLED', 'CLOSED_BY_SALE')),
	action_type    TEXT   NOT NULL,
	note           TEXT   NOT NULL DEFAULT '',
	reason         TEXT   NOT NULL DEFAULT '',
	actor_id       TEXT   NOT NULL DEFAULT '',
	correlation_id TEXT   NOT NULL DEFAULT '',
	occurred_at    TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (vehicle_id, version)
);

-- Projection bookkeeping: last aggregate version applied, committed in the same
-- transaction as the projected state.
CREATE TABLE projection_aggregate (
	aggregate_id  TEXT PRIMARY KEY,
	version       BIGINT NOT NULL CHECK (version >= 1),
	last_event_id BIGINT NOT NULL,
	updated_at    TIMESTAMPTZ NOT NULL
);

-- One row per projection run (sync or rebuild): when the read model was last brought
-- up to date and from which event-store snapshot.
CREATE TABLE projection_run (
	run_id         BIGSERIAL PRIMARY KEY,
	kind           TEXT NOT NULL CHECK (kind IN ('sync', 'rebuild')),
	started_at     TIMESTAMPTZ NOT NULL,
	finished_at    TIMESTAMPTZ NOT NULL,
	events_read    BIGINT NOT NULL,
	events_applied BIGINT NOT NULL
);
`},
}

// Migrate applies the read-model schema. PostgreSQL DDL is transactional, so each
// version is applied and recorded atomically; concurrent callers are serialised with
// a transaction-scoped advisory lock.
func Migrate(db *gorm.DB) error {
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS rm_schema_migrations (
		version    INT PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`).Error; err != nil {
		return fmt.Errorf("read model migrate: %w", err)
	}
	latest := migrations[len(migrations)-1].version
	for _, m := range migrations {
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(`SELECT pg_advisory_xact_lock(?, 0)`, lockClassMigrate).Error; err != nil {
				return err
			}
			var newest int
			if err := tx.Raw(`SELECT COALESCE(max(version), 0) FROM rm_schema_migrations`).Scan(&newest).Error; err != nil {
				return err
			}
			if newest > latest {
				return fmt.Errorf("database has read-model schema version %d, this build knows up to %d", newest, latest)
			}
			if newest >= m.version {
				return nil
			}
			if err := tx.Exec(m.sql).Error; err != nil {
				return err
			}
			return tx.Exec(`INSERT INTO rm_schema_migrations(version, name) VALUES (?, ?)`, m.version, m.name).Error
		})
		if err != nil {
			return fmt.Errorf("read model migrate: version %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}
