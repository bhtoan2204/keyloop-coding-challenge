package viewstore

import (
	"reflect"
	"strings"
	"testing"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
)

var bd = domain.NewDate(2026, 10, 29)

func TestListSQLAgingCutoff(t *testing.T) {
	sqlText, args := listVehiclesSQL(query.VehicleFilter{DealershipID: "d-001", AgingOnly: true, BusinessDate: bd, Limit: 21})
	if !strings.Contains(sqlText, "status = 'IN_STOCK' AND stocked_on <= CAST(? AS date)") {
		t.Fatalf("aging predicate: %s", sqlText)
	}
	// age > 90  <=>  stocked_on <= bd - 91: 2026-07-30 is 91 days before 2026-10-29.
	if want := []interface{}{"d-001", "2026-07-30", 21}; !reflect.DeepEqual(args, want) {
		t.Fatalf("args %v, want %v", args, want)
	}
	if domain.AgeInDays(domain.NewDate(2026, 7, 30), bd) != 91 || domain.AgeInDays(domain.NewDate(2026, 7, 31), bd) != 90 {
		t.Fatal("cutoff date is not the 91-day boundary")
	}
}

func TestListSQLAgeBoundsAreSargable(t *testing.T) {
	min, max := 89, 91
	sqlText, args := listVehiclesSQL(query.VehicleFilter{DealershipID: "d-001", Statuses: []domain.InventoryStatus{domain.StatusInStock},
		MinAgeDays: &min, MaxAgeDays: &max, BusinessDate: bd, Limit: 5})
	if !strings.Contains(sqlText, "stocked_on <= CAST(? AS date) AND stocked_on >= CAST(? AS date)") || strings.Contains(sqlText, "SOLD") {
		t.Fatalf("in-stock age bounds: %s", sqlText)
	}
	// min 89 -> stocked_on <= bd-89 ; max 91 -> stocked_on >= bd-91
	if want := []interface{}{"d-001", "IN_STOCK", "2026-08-01", "2026-07-30", 5}; !reflect.DeepEqual(args, want) {
		t.Fatalf("args %v, want %v", args, want)
	}
	all, _ := listVehiclesSQL(query.VehicleFilter{DealershipID: "d-001", MinAgeDays: &min, BusinessDate: bd, Limit: 5})
	if !strings.Contains(all, "(sold_on - stocked_on) >= ?") || !strings.Contains(all, " OR ") {
		t.Fatalf("ALL statuses must add the sold-age branch: %s", all)
	}
}

func TestListSQLKeysetAndOrder(t *testing.T) {
	after := &query.Keyset{StockedOn: domain.NewDate(2026, 7, 1), VehicleID: "v9"}
	asc, args := listVehiclesSQL(query.VehicleFilter{DealershipID: "d", After: after, BusinessDate: bd, Limit: 3})
	if !strings.Contains(asc, "(stocked_on, vehicle_id) > (CAST(? AS date), ?)") || !strings.HasSuffix(asc, "ORDER BY stocked_on ASC, vehicle_id ASC LIMIT ?") {
		t.Fatalf("ascending: %s", asc)
	}
	if args[1] != "2026-07-01" || args[2] != "v9" {
		t.Fatalf("keyset args %v", args)
	}
	desc, _ := listVehiclesSQL(query.VehicleFilter{DealershipID: "d", After: after, Descending: true, BusinessDate: bd, Limit: 3})
	if !strings.Contains(desc, "(stocked_on, vehicle_id) < (CAST(? AS date), ?)") || !strings.HasSuffix(desc, "ORDER BY stocked_on DESC, vehicle_id DESC LIMIT ?") {
		t.Fatalf("descending: %s", desc)
	}
}

func TestListSQLNeverEmbedsUserText(t *testing.T) {
	evil := "x' OR '1'='1"
	sqlText, args := listVehiclesSQL(query.VehicleFilter{DealershipID: evil, Make: evil, Model: evil, BusinessDate: bd, Limit: 1})
	if strings.Contains(sqlText, evil) {
		t.Fatalf("user text in SQL: %s", sqlText)
	}
	if args[0] != evil || args[1] != evil || args[2] != evil {
		t.Fatalf("user text must be bound parameters: %v", args)
	}
}
