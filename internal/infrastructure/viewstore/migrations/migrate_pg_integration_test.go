//go:build integration

package migrations_test

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/viewstore/migrations"
	"keyloop-coding-challenge/internal/testsupport/pgtest"
)

func eventStoreDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, _ := pgtest.NewDatabase(t)
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func tables(t *testing.T, db *gorm.DB, schema string) []string {
	t.Helper()
	var names []string
	err := db.Raw(`SELECT table_name FROM information_schema.tables WHERE table_schema = ? ORDER BY table_name`, schema).
		Scan(&names).Error
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func versions(t *testing.T, db *gorm.DB) []int {
	t.Helper()
	var v []int
	if err := db.Raw(`SELECT version FROM read_model.schema_migrations ORDER BY version`).Scan(&v).Error; err != nil {
		t.Fatal(err)
	}
	return v
}

func exec(t *testing.T, db *gorm.DB, sql string) {
	t.Helper()
	if err := db.Exec(sql).Error; err != nil {
		t.Fatal(err)
	}
}

func TestMigrateCreatesTheCurrentLayout(t *testing.T) {
	db := eventStoreDB(t)
	if err := migrations.Migrate(db); err != nil {
		t.Fatal(err)
	}
	wantEventStore := []string{"dealership", "es_aggregate", "es_aggregate_snapshot", "es_event", "schema_migrations"}
	wantReadModel := []string{"inventory_vehicle", "projection_aggregate", "projection_run", "schema_migrations", "vehicle_action_history"}
	if got := tables(t, db, "event_store"); !reflect.DeepEqual(got, wantEventStore) {
		t.Fatalf("event_store tables %v", got)
	}
	if got := tables(t, db, "read_model"); !reflect.DeepEqual(got, wantReadModel) {
		t.Fatalf("read_model tables %v", got)
	}
	if got := tables(t, db, "public"); len(got) != 0 {
		t.Fatalf("public tables %v", got)
	}
	if got := versions(t, db); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("versions %v", got)
	}
	var target string
	err := db.Raw(`SELECT confrelid::regclass::text FROM pg_constraint WHERE conname = 'inventory_vehicle_dealership_fk'`).
		Scan(&target).Error
	if err != nil || target != "event_store.dealership" {
		t.Fatalf("dealership foreign key targets %q (%v)", target, err)
	}
}

// TestMigrateAgainKeepsData: re-running is a no-op on a populated current layout.
func TestMigrateAgainKeepsData(t *testing.T) {
	db := eventStoreDB(t)
	if err := migrations.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO read_model.inventory_vehicle(vehicle_id, dealership_id, vin, make, model, model_year, stocked_on,
		status, version, last_event_id, updated_at)
		VALUES ('v1', 'd-001', '1HGCM82633A004352', 'Toyota', 'Corolla', 2023, DATE '2026-06-01', 'IN_STOCK', 1, 1, now())`)
	exec(t, db, `INSERT INTO read_model.projection_aggregate VALUES ('v1', 1, 1, now())`)
	for i := 0; i < 2; i++ {
		if err := migrations.Migrate(db); err != nil {
			t.Fatalf("run %d: %v", i+2, err)
		}
	}
	var vehicles, applied int
	db.Raw(`SELECT count(*) FROM read_model.inventory_vehicle`).Scan(&vehicles)
	db.Raw(`SELECT count(*) FROM read_model.projection_aggregate`).Scan(&applied)
	if vehicles != 1 || applied != 1 || !reflect.DeepEqual(versions(t, db), []int{1}) {
		t.Fatalf("after re-runs: vehicles %d, applied %d, versions %v", vehicles, applied, versions(t, db))
	}
}

func TestMigrateConcurrentCallers(t *testing.T) {
	db := eventStoreDB(t)
	const callers = 8
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = migrations.Migrate(db)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if got := versions(t, db); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("versions %v", got)
	}
}

// TestMigrateFailedVersionRollsBack: without the event store the version fails, and it
// leaves no table and no recorded version behind.
func TestMigrateFailedVersionRollsBack(t *testing.T) {
	db, _ := pgtest.NewDatabase(t)
	err := migrations.Migrate(db)
	if err == nil || !strings.Contains(err.Error(), "migrate the event store first") {
		t.Fatalf("got %v", err)
	}
	if got := tables(t, db, "read_model"); !reflect.DeepEqual(got, []string{"schema_migrations"}) {
		t.Fatalf("read_model tables after a failed version: %v", got)
	}
	if got := versions(t, db); len(got) != 0 {
		t.Fatalf("versions recorded: %v", got)
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	db := eventStoreDB(t)
	if err := migrations.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO read_model.schema_migrations(version, name) VALUES (99, 'from a newer release')`)
	if err := migrations.Migrate(db); err == nil || !strings.Contains(err.Error(), "schema version 99") {
		t.Fatalf("got %v", err)
	}
}

// TestMigrateRefusesAnEarlierPublicLayout: read-model tables left in public by an
// earlier layout are not adopted, moved or dropped; migrate fails and leaves them.
func TestMigrateRefusesAnEarlierPublicLayout(t *testing.T) {
	db := eventStoreDB(t)
	exec(t, db, `CREATE TABLE public.inventory_vehicle (vehicle_id TEXT PRIMARY KEY)`)
	exec(t, db, `INSERT INTO public.inventory_vehicle VALUES ('old-1')`)
	err := migrations.Migrate(db)
	if err == nil || !strings.Contains(err.Error(), "earlier layout") {
		t.Fatalf("got %v", err)
	}
	var kept int
	db.Raw(`SELECT count(*) FROM public.inventory_vehicle`).Scan(&kept)
	if kept != 1 {
		t.Fatalf("earlier rows: %d", kept)
	}
	if got := tables(t, db, "read_model"); !reflect.DeepEqual(got, []string{"schema_migrations"}) {
		t.Fatalf("read_model tables: %v", got)
	}
}
