//go:build integration

package repositories

import (
	"database/sql"
	"strings"
	"testing"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/viewstore/migrations"
	"keyloop-coding-challenge/internal/infrastructure/viewstore/models"
	"keyloop-coding-challenge/internal/testsupport/pgtest"
)

// TestPrincipalQueriesCanUseTheirIndexes inspects the plans of the main list queries.
// On a small table the planner may rightly prefer a sequential scan, so the plan is
// checked with enable_seqscan = off: that asserts the index is APPLICABLE to the
// predicates and ordering (not which plan wins at a given size). The natural plan is
// logged for information.
func TestPrincipalQueriesCanUseTheirIndexes(t *testing.T) {
	db, _ := pgtest.NewDatabase(t)
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO read_model.inventory_vehicle(vehicle_id, dealership_id, vin, make, model, model_year, stocked_on, status,
			sold_on, version, last_event_id, updated_at)
		SELECT 'v' || i, CASE WHEN i % 2 = 0 THEN 'd-001' ELSE 'd-002' END, '1HGCM82633A004352',
			(ARRAY['Toyota','Mazda','Ford'])[1 + i % 3], 'M' || (i % 7), 2023,
			DATE '2026-10-29' - (i % 400), CASE WHEN i % 10 = 0 THEN 'SOLD' ELSE 'IN_STOCK' END,
			CASE WHEN i % 10 = 0 THEN DATE '2026-10-29' - (i % 400) + 10 END, 1, i, now()
		FROM generate_series(1, 5000) i;
		ANALYZE read_model.inventory_vehicle;`).Error; err != nil {
		t.Fatal(err)
	}
	bd := domain.NewDate(2026, 10, 29)
	in := []domain.InventoryStatus{domain.StatusInStock}
	cases := []struct {
		name  string
		f     query.VehicleFilter
		index string
	}{
		{"default list", query.VehicleFilter{DealershipID: "d-001", Statuses: in, BusinessDate: bd, Limit: 21}, "ix_vehicle_dealer_status_stocked"},
		{"aging only, next page", query.VehicleFilter{DealershipID: "d-001", Statuses: in, AgingOnly: true, BusinessDate: bd, Limit: 21,
			After: &query.Keyset{StockedOn: bd.AddDays(-200), VehicleID: "v100"}}, "ix_vehicle_dealer_status_stocked"},
		{"youngest first", query.VehicleFilter{DealershipID: "d-001", Statuses: in, Descending: true, BusinessDate: bd, Limit: 21}, "ix_vehicle_dealer_status_stocked"},
		{"make + model", query.VehicleFilter{DealershipID: "d-001", Statuses: in, Make: "Toyota", Model: "M3", BusinessDate: bd, Limit: 21}, "ix_vehicle_dealer_make_model"},
		{"min/max age", query.VehicleFilter{DealershipID: "d-001", Statuses: in, MinAgeDays: &[]int{30}[0], MaxAgeDays: &[]int{120}[0], BusinessDate: bd, Limit: 21}, "ix_vehicle_dealer_status_stocked"},
		{"aging only, first page", query.VehicleFilter{DealershipID: "d-001", Statuses: in, AgingOnly: true, BusinessDate: bd, Limit: 21}, "ix_vehicle_dealer_status_stocked"},
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The exact statement List sends, rendered without running it.
			var rows []models.InventoryVehicle
			stmt := listVehicles(db.Session(&gorm.Session{DryRun: true}), tc.f).Find(&rows).Statement
			sqlText, args := stmt.SQL.String(), stmt.Vars
			t.Logf("natural plan:\n%s", explain(t, sqlDB, "EXPLAIN ANALYZE "+sqlText, args))
			tx, err := sqlDB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec("SET LOCAL enable_seqscan = off"); err != nil {
				t.Fatal(err)
			}
			plan := explain(t, tx, "EXPLAIN "+sqlText, args)
			if !strings.Contains(plan, tc.index) {
				t.Fatalf("index %s not usable:\n%s", tc.index, plan)
			}
			// Age filters must not wrap stocked_on in an expression (sargability).
			if strings.Contains(sqlText, "stocked_on +") || strings.Contains(sqlText, "- stocked_on") && !strings.Contains(sqlText, "sold_on - stocked_on") {
				t.Fatalf("non-sargable stocked_on expression: %s", sqlText)
			}
		})
	}
}

type queryer interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

// explain runs an EXPLAIN through database/sql: the rendered statement uses $n
// placeholders, which GORM's Raw would not bind.
func explain(t *testing.T, q queryer, statement string, args []interface{}) string {
	t.Helper()
	rows, err := q.Query(statement, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}
