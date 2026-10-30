//go:build integration

package viewstore_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/internal/testsupport/pgtest"
)

// legacyReadModelDDL is version 1 of the read model as Phases 4-6 created it in the
// public schema (the only version that ever shipped), recorded in rm_schema_migrations.
const legacyReadModelDDL = `
CREATE TABLE rm_schema_migrations (
	version    INT PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO rm_schema_migrations(version, name) VALUES (1, 'read model tables');
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
`

func legacyDatabase(t *testing.T, dealership string) *gorm.DB {
	t.Helper()
	db, _ := pgtest.NewDatabase(t)
	exec(t, db, legacyReadModelDDL)
	exec(t, db, `INSERT INTO dealership VALUES ('d-009', 'Legacy only', 'UTC') ON CONFLICT DO NOTHING`)
	exec(t, db, `
		INSERT INTO inventory_vehicle(vehicle_id, dealership_id, vin, make, model, model_year, stocked_on, status,
			action_id, action_type, action_note, action_proposed_at, action_updated_at, action_updated_by,
			version, last_event_id, updated_at)
		VALUES ('v1', ?, '1HGCM82633A004352', 'Toyota', 'Corolla', 2023, DATE '2026-06-01', 'IN_STOCK',
			'a1', 'HOLD', 'n', now(), now(), 'manager-1', 2, 42, now())`, dealership)
	exec(t, db, `
		INSERT INTO vehicle_action_history(vehicle_id, version, event_id, action_id, change, action_type, occurred_at)
		VALUES ('v1', 2, 42, 'a1', 'PROPOSED', 'HOLD', now());
		INSERT INTO projection_aggregate VALUES ('v1', 2, 42, now());
		INSERT INTO projection_run(kind, started_at, finished_at, events_read, events_applied)
		VALUES ('rebuild', now(), now(), 2, 2);`)
	return db
}

func readModelSnapshot(t *testing.T, db *gorm.DB, schema string) string {
	t.Helper()
	var parts []string
	for _, q := range []string{
		`SELECT vehicle_id, dealership_id, action_id, version, last_event_id FROM %s.inventory_vehicle ORDER BY vehicle_id`,
		`SELECT vehicle_id, version, event_id, change FROM %s.vehicle_action_history ORDER BY vehicle_id, version`,
		`SELECT aggregate_id, version, last_event_id FROM %s.projection_aggregate ORDER BY aggregate_id`,
		`SELECT run_id, kind, events_read FROM %s.projection_run ORDER BY run_id`,
	} {
		var rows []map[string]interface{}
		if err := db.Raw(strings.ReplaceAll(q, "%s", schema)).Scan(&rows).Error; err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			delete(r, "updated_at")
		}
		parts = append(parts, fmt.Sprint(rows))
	}
	return strings.Join(parts, " | ")
}

// TestMigrateAdoptsTheEarlierPublicReadModel: an existing Phase 4-6 database is upgraded
// in place: its read-model rows move to read_model unchanged, the dealership foreign key
// now points at event_store.dealership, nothing is dropped, and a second run is a no-op.
func TestMigrateAdoptsTheEarlierPublicReadModel(t *testing.T) {
	db := legacyDatabase(t, "d-001")
	before := readModelSnapshot(t, db, "public")
	migrateBoth(t, db)

	if after := readModelSnapshot(t, db, "read_model"); after != before {
		t.Fatalf("rows changed by the upgrade\nbefore %s\nafter  %s", before, after)
	}
	for table, want := range map[string]bool{
		"public.inventory_vehicle":    false,
		"public.projection_run":       false,
		"public.dealership":           true,
		"public.rm_schema_migrations": true,
	} {
		var exists bool
		db.Raw(`SELECT to_regclass(?) IS NOT NULL`, table).Scan(&exists)
		if exists != want {
			t.Errorf("%s exists=%v after the upgrade, want %v", table, exists, want)
		}
	}
	// The foreign key now checks event_store.dealership (d-009 is only in public.dealership).
	err := db.Exec(`UPDATE read_model.inventory_vehicle SET dealership_id = 'd-009'`).Error
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("dealership outside event_store.dealership: want foreign key violation, got %v", err)
	}
	// The sequence moved with projection_run: new runs continue after the old ids.
	exec(t, db, `INSERT INTO read_model.projection_run(kind, started_at, finished_at, events_read, events_applied) VALUES ('rebuild', now(), now(), 0, 0)`)
	var runs int
	db.Raw(`SELECT max(run_id) FROM read_model.projection_run`).Scan(&runs)
	if runs != 2 {
		t.Fatalf("projection_run ids continue at %d, want 2", runs)
	}

	migrateBoth(t, db)
	var versions []int
	db.Raw(`SELECT version FROM read_model.schema_migrations ORDER BY version`).Scan(&versions)
	if len(versions) != 1 || versions[0] != 1 {
		t.Fatalf("read_model versions after a second run: %v", versions)
	}
}

// TestMigrateUpgradeFailsAtomically: a legacy row whose dealership is not in
// event_store.dealership fails the upgrade, and the earlier layout is left exactly as it
// was (nothing moved).
func TestMigrateUpgradeFailsAtomically(t *testing.T) {
	db := legacyDatabase(t, "d-009")
	before := readModelSnapshot(t, db, "public")
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := viewstore.Migrate(db); err == nil {
		t.Fatal("upgrade succeeded although a vehicle references an unknown dealership")
	}
	if after := readModelSnapshot(t, db, "public"); after != before {
		t.Fatalf("a failed upgrade changed the earlier tables\nbefore %s\nafter  %s", before, after)
	}
	var moved bool
	db.Raw(`SELECT to_regclass('read_model.inventory_vehicle') IS NOT NULL`).Scan(&moved)
	if moved {
		t.Fatal("read_model.inventory_vehicle exists after a failed upgrade")
	}
}

func TestMigrateRefusesAPartialEarlierLayout(t *testing.T) {
	db := legacyDatabase(t, "d-001")
	exec(t, db, `DROP TABLE public.projection_run`)
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := viewstore.Migrate(db); err == nil || !strings.Contains(err.Error(), "refusing to guess") {
		t.Fatalf("got %v", err)
	}
}

func migrateBoth(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := viewstore.Migrate(db); err != nil {
		t.Fatal(err)
	}
}

func exec(t *testing.T, db *gorm.DB, sql string, args ...interface{}) {
	t.Helper()
	if err := db.Exec(sql, args...).Error; err != nil {
		t.Fatal(err)
	}
}
