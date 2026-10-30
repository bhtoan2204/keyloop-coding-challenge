package viewstore

import (
	"context"
	"fmt"
	"strings"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
)

// ListVehicles runs the list query built from fixed predicates and two fixed
// orderings; every value is a bound parameter.
func (p *Postgres) ListVehicles(ctx context.Context, f query.VehicleFilter) ([]query.Vehicle, error) {
	sqlText, args := listVehiclesSQL(f)
	rows, err := p.db.WithContext(ctx).Raw(sqlText, args...).Rows()
	if err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	defer rows.Close()
	var out []query.Vehicle
	for rows.Next() {
		r, err := scanVehicle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, toQueryVehicle(r))
	}
	return out, rows.Err()
}

func listVehiclesSQL(f query.VehicleFilter) (string, []interface{}) {
	where := []string{"dealership_id = ?"}
	args := []interface{}{f.DealershipID}
	if len(f.Statuses) == 1 {
		where = append(where, "status = ?")
		args = append(args, string(f.Statuses[0]))
	}
	if f.Make != "" {
		where = append(where, "lower(make) = lower(?)")
		args = append(args, f.Make)
	}
	if f.Model != "" {
		where = append(where, "lower(model) = lower(?)")
		args = append(args, f.Model)
	}
	if f.MinAgeDays != nil || f.MaxAgeDays != nil {
		predicate, ageArgs := ageBoundsPredicate(f)
		where = append(where, predicate)
		args = append(args, ageArgs...)
	}
	if f.AgingOnly {
		// age > 90  <=>  stocked_on <= business_date - 91; never a sold vehicle.
		where = append(where, "status = 'IN_STOCK'", "stocked_on <= CAST(? AS date)")
		args = append(args, f.BusinessDate.AddDays(-(domain.AgingThresholdDays + 1)).String())
	}
	order := "stocked_on ASC, vehicle_id ASC"
	if f.Descending {
		order = "stocked_on DESC, vehicle_id DESC"
	}
	if f.After != nil {
		op := ">"
		if f.Descending {
			op = "<"
		}
		where = append(where, "(stocked_on, vehicle_id) "+op+" (CAST(? AS date), ?)")
		args = append(args, f.After.StockedOn.String(), f.After.VehicleID)
	}
	args = append(args, f.Limit)
	sqlText := `SELECT ` + vehicleColumns + ` FROM read_model.inventory_vehicle WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY ` + order + ` LIMIT ?`
	return sqlText, args
}

// ageBoundsPredicate returns one OR-branch per requested status. In-stock age bounds
// become range predicates on stocked_on (index-friendly); a sold vehicle's age is
// fixed at its sale date, so its bounds use sold_on - stocked_on.
func ageBoundsPredicate(f query.VehicleFilter) (string, []interface{}) {
	inStock, sold := len(f.Statuses) == 0, len(f.Statuses) == 0
	for _, s := range f.Statuses {
		inStock = inStock || s == domain.StatusInStock
		sold = sold || s == domain.StatusSold
	}
	var branches []string
	var args []interface{}
	if inStock {
		branch := []string{"status = 'IN_STOCK'"}
		if f.MinAgeDays != nil {
			branch = append(branch, "stocked_on <= CAST(? AS date)")
			args = append(args, f.BusinessDate.AddDays(-*f.MinAgeDays).String())
		}
		if f.MaxAgeDays != nil {
			branch = append(branch, "stocked_on >= CAST(? AS date)")
			args = append(args, f.BusinessDate.AddDays(-*f.MaxAgeDays).String())
		}
		branches = append(branches, "("+strings.Join(branch, " AND ")+")")
	}
	if sold {
		branch := []string{"status = 'SOLD'"}
		if f.MinAgeDays != nil {
			branch = append(branch, "(sold_on - stocked_on) >= ?")
			args = append(args, *f.MinAgeDays)
		}
		if f.MaxAgeDays != nil {
			branch = append(branch, "(sold_on - stocked_on) <= ?")
			args = append(args, *f.MaxAgeDays)
		}
		branches = append(branches, "("+strings.Join(branch, " AND ")+")")
	}
	return "(" + strings.Join(branches, " OR ") + ")", args
}
