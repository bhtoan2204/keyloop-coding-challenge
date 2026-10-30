package repositories

import (
	"reflect"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/infrastructure/viewstore/models"
)

var bd = domain.NewDate(2026, 10, 29)

// listSQL renders the composed list query (PostgreSQL dialect) without a database.
func listSQL(t *testing.T, f query.VehicleFilter) (string, []interface{}) {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=127.0.0.1 port=1"}), &gorm.Config{
		DryRun:               true,
		DisableAutomaticPing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var rows []models.InventoryVehicle
	stmt := listVehicles(db, f).Find(&rows).Statement
	return stmt.SQL.String(), stmt.Vars
}

func TestListSQLAgingCutoff(t *testing.T) {
	sqlText, args := listSQL(t, query.VehicleFilter{DealershipID: "d-001", AgingOnly: true, BusinessDate: bd, Limit: 21})
	if !strings.Contains(sqlText, "status = 'IN_STOCK' AND stocked_on <= CAST($2 AS date)") {
		t.Fatalf("aging predicate: %s", sqlText)
	}
	// age > 90  <=>  stocked_on <= bd - 91: 2026-07-30 is 91 days before 2026-10-29.
	if want := []interface{}{"d-001", "2026-07-30", 21}; !reflect.DeepEqual(args, want) {
		t.Fatalf("args %v, want %v", args, want)
	}
	if !strings.HasSuffix(sqlText, "LIMIT $3") {
		t.Fatalf("limit: %s", sqlText)
	}
	if domain.AgeInDays(domain.NewDate(2026, 7, 30), bd) != 91 || domain.AgeInDays(domain.NewDate(2026, 7, 31), bd) != 90 {
		t.Fatal("cutoff date is not the 91-day boundary")
	}
}

func TestListSQLAgeBoundsAreSargable(t *testing.T) {
	minAge, maxAge := 89, 91
	sqlText, args := listSQL(t, query.VehicleFilter{DealershipID: "d-001", Statuses: []domain.InventoryStatus{domain.StatusInStock},
		MinAgeDays: &minAge, MaxAgeDays: &maxAge, BusinessDate: bd, Limit: 5})
	// The single age branch is AND-joined inside the dealership scope, never OR-joined.
	if !strings.Contains(sqlText, "WHERE dealership_id = $1 AND status = $2 AND (status = 'IN_STOCK' AND stocked_on <= CAST($3 AS date) AND stocked_on >= CAST($4 AS date)) ORDER BY") ||
		strings.Contains(sqlText, " OR ") || strings.Contains(sqlText, "SOLD") {
		t.Fatalf("in-stock age bounds: %s", sqlText)
	}
	sold, _ := listSQL(t, query.VehicleFilter{DealershipID: "d-001", Statuses: []domain.InventoryStatus{domain.StatusSold},
		MinAgeDays: &minAge, BusinessDate: bd, Limit: 5})
	if !strings.Contains(sold, "WHERE dealership_id = $1 AND status = $2 AND (status = 'SOLD' AND (sold_on - stocked_on) >= $3) ORDER BY") || strings.Contains(sold, " OR ") {
		t.Fatalf("sold age bounds: %s", sold)
	}
	// min 89 -> stocked_on <= bd-89 ; max 91 -> stocked_on >= bd-91
	if want := []interface{}{"d-001", "IN_STOCK", "2026-08-01", "2026-07-30", 5}; !reflect.DeepEqual(args, want) {
		t.Fatalf("args %v, want %v", args, want)
	}
	all, allArgs := listSQL(t, query.VehicleFilter{DealershipID: "d-001", MinAgeDays: &minAge, BusinessDate: bd, Limit: 5})
	if !strings.Contains(all, "(sold_on - stocked_on) >= $3") || !strings.Contains(all, " OR ") {
		t.Fatalf("ALL statuses must add the sold-age branch: %s", all)
	}
	// The OR must be grouped, or it would escape the dealership scope.
	if !strings.Contains(all, "dealership_id = $1 AND ((") {
		t.Fatalf("age branches are not grouped under the dealership scope: %s", all)
	}
	if want := []interface{}{"d-001", "2026-08-01", 89, 5}; !reflect.DeepEqual(allArgs, want) {
		t.Fatalf("args %v, want %v", allArgs, want)
	}
}

func TestListSQLKeysetAndOrder(t *testing.T) {
	after := &query.Keyset{StockedOn: domain.NewDate(2026, 7, 1), VehicleID: "v9"}
	asc, args := listSQL(t, query.VehicleFilter{DealershipID: "d", After: after, BusinessDate: bd, Limit: 3})
	if !strings.Contains(asc, "(stocked_on, vehicle_id) > (CAST($2 AS date), $3)") || !strings.HasSuffix(asc, "ORDER BY stocked_on ASC, vehicle_id ASC LIMIT $4") {
		t.Fatalf("ascending: %s", asc)
	}
	if args[1] != "2026-07-01" || args[2] != "v9" {
		t.Fatalf("keyset args %v", args)
	}
	desc, _ := listSQL(t, query.VehicleFilter{DealershipID: "d", After: after, Descending: true, BusinessDate: bd, Limit: 3})
	if !strings.Contains(desc, "(stocked_on, vehicle_id) < (CAST($2 AS date), $3)") || !strings.HasSuffix(desc, "ORDER BY stocked_on DESC, vehicle_id DESC LIMIT $4") {
		t.Fatalf("descending: %s", desc)
	}
}

func TestListSQLNeverEmbedsUserText(t *testing.T) {
	evil := "x' OR '1'='1"
	sqlText, args := listSQL(t, query.VehicleFilter{DealershipID: evil, Make: evil, Model: evil, BusinessDate: bd, Limit: 1})
	if strings.Contains(sqlText, evil) {
		t.Fatalf("user text in SQL: %s", sqlText)
	}
	if want := []interface{}{evil, evil, evil, 1}; !reflect.DeepEqual(args, want) {
		t.Fatalf("user text must be bound parameters: %v", args)
	}
}
