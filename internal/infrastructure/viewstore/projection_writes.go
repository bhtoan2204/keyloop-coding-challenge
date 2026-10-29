package viewstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (p *Postgres) LockAggregate(ctx context.Context, aggregateID string) (int, error) {
	if !p.inTx {
		return 0, ErrNotInTransaction
	}
	if !p.exclusive {
		// Shared projection lock: waits while a sync/rebuild runs. Then the aggregate
		// lock: a row lock cannot protect a row that does not exist yet (first event of
		// a new vehicle); a transaction-scoped advisory lock on the aggregate ID can.
		if err := p.db.WithContext(ctx).Exec(`SELECT pg_advisory_xact_lock_shared(?, 0), pg_advisory_xact_lock(?, hashtext(CAST(? AS text)))`,
			lockClassProjection, lockClassAggregate, aggregateID).Error; err != nil {
			return 0, fmt.Errorf("lock aggregate %s: %w", aggregateID, err)
		}
	}
	var version int
	err := p.db.WithContext(ctx).Raw(`SELECT version FROM projection_aggregate WHERE aggregate_id = ?`, aggregateID).
		Row().Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read applied version of %s: %w", aggregateID, err)
	}
	return version, nil
}

func (p *Postgres) SetAppliedVersion(ctx context.Context, aggregateID string, version int, eventID int64) error {
	if !p.inTx {
		return ErrNotInTransaction
	}
	return p.db.WithContext(ctx).Exec(`
		INSERT INTO projection_aggregate(aggregate_id, version, last_event_id, updated_at) VALUES (?, ?, ?, now())
		ON CONFLICT (aggregate_id) DO UPDATE
			SET version = EXCLUDED.version, last_event_id = EXCLUDED.last_event_id, updated_at = EXCLUDED.updated_at`,
		aggregateID, version, eventID).Error
}

func (p *Postgres) Vehicle(ctx context.Context, vehicleID string) (*VehicleRow, error) {
	rows, err := p.db.WithContext(ctx).Raw(`SELECT `+vehicleColumns+` FROM inventory_vehicle WHERE vehicle_id = ?`, vehicleID).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	row, err := scanVehicle(rows)
	if err != nil {
		return nil, err
	}
	return &row, rows.Err()
}

func (p *Postgres) SaveVehicle(ctx context.Context, r VehicleRow) error {
	if !p.inTx {
		return ErrNotInTransaction
	}
	return p.db.WithContext(ctx).Exec(`
		INSERT INTO inventory_vehicle(`+vehicleColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, CAST(? AS date), ?, CAST(? AS date), ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (vehicle_id) DO UPDATE SET
			dealership_id = EXCLUDED.dealership_id, vin = EXCLUDED.vin, make = EXCLUDED.make,
			model = EXCLUDED.model, model_year = EXCLUDED.model_year, stocked_on = EXCLUDED.stocked_on,
			status = EXCLUDED.status, sold_on = EXCLUDED.sold_on, action_id = EXCLUDED.action_id,
			action_type = EXCLUDED.action_type, action_note = EXCLUDED.action_note,
			action_proposed_at = EXCLUDED.action_proposed_at, action_updated_at = EXCLUDED.action_updated_at,
			action_updated_by = EXCLUDED.action_updated_by, version = EXCLUDED.version,
			last_event_id = EXCLUDED.last_event_id, updated_at = EXCLUDED.updated_at`,
		r.VehicleID, r.DealershipID, r.VIN, r.Make, r.Model, r.ModelYear, r.StockedOn.String(), string(r.Status),
		nullDate(r.SoldOn), nullString(r.ActionID), nullString(string(r.ActionType)), nullString(r.ActionNote),
		nullTime(r.ActionProposedAt), nullTime(r.ActionUpdatedAt), nullString(r.ActionUpdatedBy),
		r.Version, r.LastEventID, r.UpdatedAt).Error
}

func (p *Postgres) AppendHistory(ctx context.Context, h HistoryRow) error {
	if !p.inTx {
		return ErrNotInTransaction
	}
	return p.db.WithContext(ctx).Exec(`
		INSERT INTO vehicle_action_history(vehicle_id, version, event_id, action_id, change, action_type,
			note, reason, actor_id, correlation_id, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.VehicleID, h.Version, h.EventID, h.ActionID, h.Change, string(h.ActionType),
		h.Note, h.Reason, h.ActorID, h.CorrelationID, h.OccurredAt).Error
}
