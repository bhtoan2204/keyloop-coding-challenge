package viewstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"keyloop-coding-challenge/internal/domain"

	"gorm.io/gorm"
)

var _ ViewStore = (*Postgres)(nil)

// Postgres implements ViewStore (projection writes) and query.ReadModel (reads) on
// PostgreSQL through GORM with raw SQL, the same access style as the event store.
type Postgres struct {
	db   *gorm.DB
	inTx bool
	// exclusive is set inside RunExclusive: the run holds the projection lock
	// exclusively, so no other applier can run and per-aggregate locks are not needed.
	exclusive bool
}

// ErrRunInProgress: another sync/rebuild (or a live applier) holds the projection lock.
var ErrRunInProgress = errors.New("a projection run or applier is already active")

func NewPostgres(db *gorm.DB) *Postgres {
	return &Postgres{db: db}
}

func (p *Postgres) WithTransaction(ctx context.Context, fn func(tx ViewStore) error) error {
	if p.exclusive {
		// Inside a run the whole run is one all-or-nothing transaction; no savepoints.
		return fn(p)
	}
	return p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Postgres{db: tx, inTx: true})
	})
}

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

const vehicleColumns = `vehicle_id, dealership_id, vin, make, model, model_year, stocked_on, status, sold_on,
	action_id, action_type, action_note, action_proposed_at, action_updated_at, action_updated_by,
	version, last_event_id, updated_at`

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

// --- projection runs (sync / rebuild) -------------------------------------------------

// RunExclusive runs fn in ONE transaction holding the projection lock exclusively, so
// no other run or applier can interleave. With truncate the projected state is emptied
// first (rebuild). The run is recorded in projection_run in the same transaction:
// either everything commits or the read model is left exactly as it was.
func (p *Postgres) RunExclusive(ctx context.Context, kind string, truncate bool,
	fn func(tx ViewStore) (read, applied int, err error)) (read, applied int, err error) {
	started := time.Now()
	err = p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked bool
		if err := tx.Raw(`SELECT pg_try_advisory_xact_lock(?, 0)`, lockClassProjection).Row().Scan(&locked); err != nil {
			return err
		}
		if !locked {
			return ErrRunInProgress
		}
		if truncate {
			if err := tx.Exec(`TRUNCATE vehicle_action_history, inventory_vehicle, projection_aggregate`).Error; err != nil {
				return err
			}
		}
		var fnErr error
		read, applied, fnErr = fn(&Postgres{db: tx, inTx: true, exclusive: true})
		if fnErr != nil {
			return fnErr
		}
		return tx.Exec(`INSERT INTO projection_run(kind, started_at, finished_at, events_read, events_applied)
			VALUES (?, ?, now(), ?, ?)`, kind, started, read, applied).Error
	})
	if err != nil {
		return 0, 0, err
	}
	return read, applied, nil
}

// --- scanning helpers ------------------------------------------------------------------

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanVehicle(s scanner) (VehicleRow, error) {
	var (
		r                                     VehicleRow
		status                                string
		stockedOn                             time.Time
		soldOn, proposedAt, updatedAt         sql.NullTime
		actionID, actionType, note, updatedBy sql.NullString
	)
	err := s.Scan(&r.VehicleID, &r.DealershipID, &r.VIN, &r.Make, &r.Model, &r.ModelYear, &stockedOn, &status, &soldOn,
		&actionID, &actionType, &note, &proposedAt, &updatedAt, &updatedBy, &r.Version, &r.LastEventID, &r.UpdatedAt)
	if err != nil {
		return r, err
	}
	r.StockedOn = dateOf(stockedOn)
	r.Status = domain.InventoryStatus(status)
	if soldOn.Valid {
		r.SoldOn = dateOf(soldOn.Time)
	}
	r.ActionID, r.ActionType, r.ActionNote, r.ActionUpdatedBy = actionID.String, domain.ActionType(actionType.String), note.String, updatedBy.String
	if proposedAt.Valid {
		r.ActionProposedAt = proposedAt.Time.UTC()
	}
	if updatedAt.Valid {
		r.ActionUpdatedAt = updatedAt.Time.UTC()
	}
	r.UpdatedAt = r.UpdatedAt.UTC()
	return r, nil
}

func dateOf(t time.Time) domain.Date {
	return domain.NewDate(t.Year(), t.Month(), t.Day())
}

func nullDate(d domain.Date) interface{} {
	if d.IsZero() {
		return nil
	}
	return d.String()
}

func nullTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
