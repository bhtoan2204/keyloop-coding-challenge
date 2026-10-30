//go:build integration

package presentation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// TestDeployedRolesHaveLeastPrivilege: each role from deploy/postgres can do its job
// (the API run above as writer + reader, a rebuild as the rebuild role) and nothing that
// would let it rewrite history, change the schema or escalate.
func TestDeployedRolesHaveLeastPrivilege(t *testing.T) {
	m := newPostgresAPI(t, nil)
	m.stock(t, "v1", "2026-07-01")
	if r := do(t, m.srv, call{method: "POST", path: vehicles + "/v1/actions", body: `{"action_type":"HOLD"}`}); r.status != 201 {
		t.Fatalf("propose as writer: %d %s", r.status, r.rawBody)
	}
	if r := do(t, m.srv, call{method: "GET", path: vehicles + "/v1/actions"}); r.status != 200 {
		t.Fatalf("history as reader: %d %s", r.status, r.rawBody)
	}
	if res, err := m.rebuild.Rebuild(context.Background()); err != nil || res.Applied != 2 {
		t.Fatalf("rebuild as rebuild role: %+v %v", res, err)
	}

	denied := map[string]struct {
		db    *gorm.DB
		stmts []string
	}{
		"writer": {m.app, []string{
			`UPDATE event_store.es_event SET data = '{}'`,
			`DELETE FROM event_store.es_event`,
			`TRUNCATE event_store.es_event`,
			`DELETE FROM event_store.es_aggregate`,
			`UPDATE event_store.es_aggregate_snapshot SET data = '{}'`,
			`DELETE FROM event_store.es_aggregate_snapshot`,
			`INSERT INTO event_store.dealership VALUES ('d-x', 'x', 'UTC')`,
			`UPDATE event_store.dealership SET timezone = 'UTC'`,
			`DELETE FROM read_model.inventory_vehicle`,
			`DELETE FROM read_model.vehicle_action_history`,
			`UPDATE read_model.vehicle_action_history SET note = 'x'`,
			`TRUNCATE read_model.inventory_vehicle`,
			`INSERT INTO event_store.schema_migrations(version, name) VALUES (99, 'x')`,
			`INSERT INTO read_model.schema_migrations(version, name) VALUES (99, 'x')`,
			`CREATE TABLE event_store.x (id int)`,
			`CREATE TABLE read_model.x (id int)`,
			`ALTER TABLE event_store.es_event ADD COLUMN x int`,
			`DROP TABLE read_model.projection_run`,
			`CREATE ROLE y`,
		}},
		"reader": {m.reader, []string{
			`SELECT * FROM event_store.es_event`,
			`SELECT * FROM event_store.es_aggregate`,
			`INSERT INTO read_model.projection_run(kind, started_at, finished_at, events_read, events_applied) VALUES ('rebuild', now(), now(), 0, 0)`,
			`UPDATE read_model.inventory_vehicle SET status = 'SOLD'`,
			`DELETE FROM read_model.vehicle_action_history`,
			`TRUNCATE read_model.inventory_vehicle`,
			`CREATE TABLE read_model.x (id int)`,
		}},
		"rebuild": {m.rebuilder, []string{
			`INSERT INTO event_store.es_aggregate(id, version, aggregate_type) VALUES ('x', 0, 'x')`,
			`UPDATE event_store.es_event SET data = '{}'`,
			`DELETE FROM event_store.es_event`,
			`TRUNCATE event_store.es_event`,
			`UPDATE event_store.dealership SET timezone = 'UTC'`,
			`CREATE TABLE read_model.x (id int)`,
			`ALTER TABLE read_model.inventory_vehicle ADD COLUMN x int`,
			`COPY read_model.inventory_vehicle TO PROGRAM 'id'`,
		}},
	}
	for role, c := range denied {
		var super bool
		if err := c.db.Raw(`SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super).Error; err != nil || super {
			t.Fatalf("%s is superuser=%v (%v)", role, super, err)
		}
		for _, stmt := range c.stmts {
			err := c.db.Exec(stmt).Error
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
				t.Errorf("%s: %s: want insufficient_privilege (42501), got %v", role, stmt, err)
			}
		}
	}
	if got := m.eventTypes(t, "v1"); len(got) != 2 {
		t.Fatalf("events after the denied statements: %v", got)
	}
}
