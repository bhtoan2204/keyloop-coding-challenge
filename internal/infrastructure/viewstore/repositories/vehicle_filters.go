package repositories

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
)

// The list query is composed from these scopes. Each adds one fixed, parameterised
// predicate; no user value ever becomes SQL text. In-stock age bounds are range
// predicates on stocked_on, so (dealership_id, status, stocked_on, vehicle_id) and the
// make/model index stay usable; ordering is fixed to the keyset (stocked_on, vehicle_id).

const (
	inStockPredicate = "status = '" + string(domain.StatusInStock) + "'"
	soldPredicate    = "status = '" + string(domain.StatusSold) + "'"
)

func inDealership(dealershipID string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("dealership_id = ?", dealershipID)
	}
}

// withStatus filters on one status; none or both statuses means no predicate.
func withStatus(statuses []domain.InventoryStatus) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if len(statuses) != 1 {
			return db
		}
		return db.Where("status = ?", string(statuses[0]))
	}
}

// withMake and withModel are case-insensitive exact matches (lower() on both sides, as
// in the make/model index).
func withMake(make string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if make == "" {
			return db
		}
		return db.Where("lower(make) = lower(?)", make)
	}
}

func withModel(model string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if model == "" {
			return db
		}
		return db.Where("lower(model) = lower(?)", model)
	}
}

// withAgeBounds adds one OR-branch per requested status: an in-stock vehicle's age runs
// to the business date, a sold vehicle's age is fixed at its sale date.
func withAgeBounds(f query.VehicleFilter) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if f.MinAgeDays == nil && f.MaxAgeDays == nil {
			return db
		}
		var branches []clause.Expression
		if includesStatus(f.Statuses, domain.StatusInStock) {
			branches = append(branches, inStockAgeBounds(f))
		}
		if includesStatus(f.Statuses, domain.StatusSold) {
			branches = append(branches, soldAgeBounds(f))
		}
		// One branch is passed as it is: the AND-join with the dealership scope must not
		// depend on how GORM renders a one-element OR.
		if len(branches) == 1 {
			return db.Where(branches[0])
		}
		return db.Where(clause.Or(branches...))
	}
}

func inStockAgeBounds(f query.VehicleFilter) clause.Expression {
	exprs := []clause.Expression{clause.Expr{SQL: inStockPredicate}}
	if f.MinAgeDays != nil {
		exprs = append(exprs, dateExpr("stocked_on <= CAST(? AS date)", f.BusinessDate.AddDays(-*f.MinAgeDays)))
	}
	if f.MaxAgeDays != nil {
		exprs = append(exprs, dateExpr("stocked_on >= CAST(? AS date)", f.BusinessDate.AddDays(-*f.MaxAgeDays)))
	}
	return clause.And(exprs...)
}

func soldAgeBounds(f query.VehicleFilter) clause.Expression {
	exprs := []clause.Expression{clause.Expr{SQL: soldPredicate}}
	if f.MinAgeDays != nil {
		exprs = append(exprs, clause.Expr{SQL: "(sold_on - stocked_on) >= ?", Vars: []interface{}{*f.MinAgeDays}})
	}
	if f.MaxAgeDays != nil {
		exprs = append(exprs, clause.Expr{SQL: "(sold_on - stocked_on) <= ?", Vars: []interface{}{*f.MaxAgeDays}})
	}
	return clause.And(exprs...)
}

// agingOnly keeps in-stock vehicles older than the aging threshold: never a sold one,
// and exactly 90 days is not aging.
func agingOnly(enabled bool, businessDate domain.Date) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if !enabled {
			return db
		}
		return db.Where(inStockPredicate).Where("stocked_on <= CAST(? AS date)", agingCutoff(businessDate).String())
	}
}

// afterKeyset continues after the last row of the previous page in the list order.
func afterKeyset(after *query.Keyset, descending bool) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if after == nil {
			return db
		}
		if descending {
			return db.Where("(stocked_on, vehicle_id) < (CAST(? AS date), ?)", after.StockedOn.String(), after.VehicleID)
		}
		return db.Where("(stocked_on, vehicle_id) > (CAST(? AS date), ?)", after.StockedOn.String(), after.VehicleID)
	}
}

// orderByAge: oldest first (stocked_on ascending) by default; vehicle_id breaks ties so
// the order, and with it the keyset, is total.
func orderByAge(descending bool) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if descending {
			return db.Order("stocked_on DESC, vehicle_id DESC")
		}
		return db.Order("stocked_on ASC, vehicle_id ASC")
	}
}

// agingCutoff is the latest stocked_on of an aging vehicle:
// age > threshold  <=>  stocked_on <= businessDate - (threshold + 1).
func agingCutoff(businessDate domain.Date) domain.Date {
	return businessDate.AddDays(-(domain.AgingThresholdDays + 1))
}

func includesStatus(statuses []domain.InventoryStatus, s domain.InventoryStatus) bool {
	if len(statuses) == 0 {
		return true
	}
	for _, candidate := range statuses {
		if candidate == s {
			return true
		}
	}
	return false
}

func dateExpr(sql string, d domain.Date) clause.Expression {
	return clause.Expr{
		SQL:  sql,
		Vars: []interface{}{d.String()},
	}
}
