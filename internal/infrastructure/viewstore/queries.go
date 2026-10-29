package viewstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
)

var _ query.ReadModel = (*Postgres)(nil)

func (p *Postgres) DealershipLocation(ctx context.Context, dealershipID string) (*time.Location, error) {
	var zone string
	err := p.db.WithContext(ctx).Raw(`SELECT timezone FROM dealership WHERE dealership_id = ?`, dealershipID).Row().Scan(&zone)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", query.ErrDealershipNotFound, dealershipID)
	}
	if err != nil {
		return nil, fmt.Errorf("load dealership %s: %w", dealershipID, err)
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("dealership %s has invalid time zone %q: %w", dealershipID, zone, err)
	}
	return loc, nil
}

// ListVehicles builds the list query from fixed predicates and two fixed orderings;
// every value is a bound parameter. Age bounds on in-stock vehicles become range
// predicates on stocked_on (index-friendly); a sold vehicle's age is fixed at its sale
// date, so its bounds use sold_on - stocked_on.
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
	inStock, sold := len(f.Statuses) == 0, len(f.Statuses) == 0
	for _, s := range f.Statuses {
		inStock = inStock || s == domain.StatusInStock
		sold = sold || s == domain.StatusSold
	}
	if len(f.Statuses) == 1 {
		where, args = append(where, "status = ?"), append(args, string(f.Statuses[0]))
	}
	if f.Make != "" {
		where, args = append(where, "lower(make) = lower(?)"), append(args, f.Make)
	}
	if f.Model != "" {
		where, args = append(where, "lower(model) = lower(?)"), append(args, f.Model)
	}
	if f.MinAgeDays != nil || f.MaxAgeDays != nil {
		var branches []string
		if inStock {
			b, bArgs := []string{"status = 'IN_STOCK'"}, []interface{}{}
			if f.MinAgeDays != nil {
				b, bArgs = append(b, "stocked_on <= CAST(? AS date)"), append(bArgs, f.BusinessDate.AddDays(-*f.MinAgeDays).String())
			}
			if f.MaxAgeDays != nil {
				b, bArgs = append(b, "stocked_on >= CAST(? AS date)"), append(bArgs, f.BusinessDate.AddDays(-*f.MaxAgeDays).String())
			}
			branches, args = append(branches, "("+strings.Join(b, " AND ")+")"), append(args, bArgs...)
		}
		if sold {
			b, bArgs := []string{"status = 'SOLD'"}, []interface{}{}
			if f.MinAgeDays != nil {
				b, bArgs = append(b, "(sold_on - stocked_on) >= ?"), append(bArgs, *f.MinAgeDays)
			}
			if f.MaxAgeDays != nil {
				b, bArgs = append(b, "(sold_on - stocked_on) <= ?"), append(bArgs, *f.MaxAgeDays)
			}
			branches, args = append(branches, "("+strings.Join(b, " AND ")+")"), append(args, bArgs...)
		}
		where = append(where, "("+strings.Join(branches, " OR ")+")")
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
	return `SELECT ` + vehicleColumns + ` FROM inventory_vehicle WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY ` + order + ` LIMIT ?`, args
}

func (p *Postgres) GetVehicle(ctx context.Context, dealershipID, vehicleID string) (*query.Vehicle, error) {
	rows, err := p.db.WithContext(ctx).Raw(`SELECT `+vehicleColumns+` FROM inventory_vehicle WHERE vehicle_id = ? AND dealership_id = ?`,
		vehicleID, dealershipID).Rows()
	if err != nil {
		return nil, fmt.Errorf("get vehicle: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", query.ErrVehicleNotFound, vehicleID)
	}
	r, err := scanVehicle(rows)
	if err != nil {
		return nil, err
	}
	v := toQueryVehicle(r)
	return &v, nil
}

func (p *Postgres) ListHistory(ctx context.Context, vehicleID string, afterVersion, limit int) ([]query.HistoryEntry, error) {
	rows, err := p.db.WithContext(ctx).Raw(`
		SELECT version, event_id, action_id, change, action_type, note, reason, actor_id, correlation_id, occurred_at
		FROM vehicle_action_history WHERE vehicle_id = ? AND version > ? ORDER BY version LIMIT ?`,
		vehicleID, afterVersion, limit).Rows()
	if err != nil {
		return nil, fmt.Errorf("list history: %w", err)
	}
	defer rows.Close()
	var out []query.HistoryEntry
	for rows.Next() {
		var h query.HistoryEntry
		var actionType string
		if err := rows.Scan(&h.Version, &h.EventID, &h.ActionID, &h.Change, &actionType, &h.Note, &h.Reason,
			&h.ActorID, &h.CorrelationID, &h.OccurredAt); err != nil {
			return nil, err
		}
		h.ActionType = domain.ActionType(actionType)
		h.OccurredAt = h.OccurredAt.UTC()
		out = append(out, h)
	}
	return out, rows.Err()
}

func (p *Postgres) Summary(ctx context.Context, dealershipID string, businessDate domain.Date) (query.Summary, error) {
	cutoff := businessDate.AddDays(-(domain.AgingThresholdDays + 1)).String()
	var (
		s      query.Summary
		oldest sql.NullTime
	)
	err := p.db.WithContext(ctx).Raw(`
		SELECT count(*) FILTER (WHERE status = 'IN_STOCK'),
		       count(*) FILTER (WHERE status = 'IN_STOCK' AND stocked_on <= CAST(? AS date)),
		       count(*) FILTER (WHERE status = 'IN_STOCK' AND stocked_on <= CAST(? AS date) AND action_id IS NULL),
		       min(stocked_on) FILTER (WHERE status = 'IN_STOCK')
		FROM inventory_vehicle WHERE dealership_id = ?`, cutoff, cutoff, dealershipID).
		Row().Scan(&s.InStock, &s.Aging, &s.AgingWithoutAction, &oldest)
	if err != nil {
		return s, fmt.Errorf("inventory summary: %w", err)
	}
	if oldest.Valid {
		s.OldestStockedOn = dateOf(oldest.Time)
	}
	return s, nil
}

func toQueryVehicle(r VehicleRow) query.Vehicle {
	v := query.Vehicle{
		VehicleID: r.VehicleID, DealershipID: r.DealershipID, VIN: r.VIN, Make: r.Make, Model: r.Model,
		ModelYear: r.ModelYear, StockedOn: r.StockedOn, Status: r.Status, SoldOn: r.SoldOn,
		Version: r.Version, UpdatedAt: r.UpdatedAt,
	}
	if r.ActionID != "" {
		v.Action = &query.Action{ID: r.ActionID, Type: r.ActionType, Note: r.ActionNote,
			ProposedAt: r.ActionProposedAt, UpdatedAt: r.ActionUpdatedAt, UpdatedBy: r.ActionUpdatedBy}
	}
	return v
}
