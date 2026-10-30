package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/infrastructure/viewstore/models"
)

// Advisory-lock classes of the read model (two-int form avoids collisions with other
// lock users). Migrations use their own class.
const (
	lockClassAggregate  = 52002
	lockClassProjection = 52003
)

// ProjectionRepository owns the projection bookkeeping (applied versions, rebuild runs)
// and the advisory locks that order commands, appliers and rebuilds. All its locks are
// transaction-scoped: they need a repository built on a transaction.
type ProjectionRepository struct {
	db *gorm.DB
}

func NewProjectionRepository(db *gorm.DB) ProjectionRepository {
	return ProjectionRepository{db: db}
}

// LockShared takes the projection lock in shared mode: commands and appliers hold it
// until they commit, so a rebuild (exclusive) never runs alongside them.
func (r ProjectionRepository) LockShared(ctx context.Context) error {
	if err := r.db.WithContext(ctx).Exec(`SELECT pg_advisory_xact_lock_shared(?, 0)`, lockClassProjection).Error; err != nil {
		return fmt.Errorf("lock projection: %w", err)
	}
	return nil
}

// TryLockExclusive takes the projection lock exclusively without waiting; false when a
// rebuild or a command holds it.
func (r ProjectionRepository) TryLockExclusive(ctx context.Context) (bool, error) {
	var locked bool
	err := r.db.WithContext(ctx).Raw(`SELECT pg_try_advisory_xact_lock(?, 0)`, lockClassProjection).Row().Scan(&locked)
	if err != nil {
		return false, fmt.Errorf("lock projection exclusively: %w", err)
	}
	return locked, nil
}

// LockAggregate takes the shared projection lock and then a lock on the aggregate id: a
// row lock cannot protect a row that does not exist yet (the first event of a new
// vehicle); a transaction-scoped advisory lock on the id can.
func (r ProjectionRepository) LockAggregate(ctx context.Context, aggregateID string) error {
	err := r.db.WithContext(ctx).Exec(`SELECT pg_advisory_xact_lock_shared(?, 0), pg_advisory_xact_lock(?, hashtext(CAST(? AS text)))`,
		lockClassProjection, lockClassAggregate, aggregateID).Error
	if err != nil {
		return fmt.Errorf("lock aggregate %s: %w", aggregateID, err)
	}
	return nil
}

// AppliedVersion returns the last applied version of the aggregate, 0 when none.
func (r ProjectionRepository) AppliedVersion(ctx context.Context, aggregateID string) (int, error) {
	var row models.ProjectionAggregate
	err := r.db.WithContext(ctx).Where("aggregate_id = ?", aggregateID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read applied version of %s: %w", aggregateID, err)
	}
	return row.Version, nil
}

// SetAppliedVersion records the applied version; updated_at is the database's
// transaction time, like the projected rows' commit.
func (r ProjectionRepository) SetAppliedVersion(ctx context.Context, aggregateID string, version int, eventID int64) error {
	err := r.db.WithContext(ctx).Exec(`
		INSERT INTO read_model.projection_aggregate(aggregate_id, version, last_event_id, updated_at) VALUES (?, ?, ?, now())
		ON CONFLICT (aggregate_id) DO UPDATE
			SET version = EXCLUDED.version, last_event_id = EXCLUDED.last_event_id, updated_at = EXCLUDED.updated_at`,
		aggregateID, version, eventID).Error
	if err != nil {
		return fmt.Errorf("set applied version of %s: %w", aggregateID, err)
	}
	return nil
}

// Truncate empties the projected tables (not projection_run, the rebuild log).
func (r ProjectionRepository) Truncate(ctx context.Context) error {
	err := r.db.WithContext(ctx).
		Exec(`TRUNCATE read_model.vehicle_action_history, read_model.inventory_vehicle, read_model.projection_aggregate`).Error
	if err != nil {
		return fmt.Errorf("truncate read model: %w", err)
	}
	return nil
}

// RecordRun logs a completed run; finished_at is the database's now() (the start of the
// run's transaction), as before Phase 7.1.
func (r ProjectionRepository) RecordRun(ctx context.Context, kind string, startedAt time.Time, read, applied int) error {
	err := r.db.WithContext(ctx).Exec(`INSERT INTO read_model.projection_run(kind, started_at, finished_at, events_read, events_applied)
		VALUES (?, ?, now(), ?, ?)`, kind, startedAt, read, applied).Error
	if err != nil {
		return fmt.Errorf("record %s run: %w", kind, err)
	}
	return nil
}
