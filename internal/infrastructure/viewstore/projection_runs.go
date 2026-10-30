package viewstore

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ErrRunInProgress: another rebuild holds the projection lock, or a command save holds it shared.
var ErrRunInProgress = errors.New("a projection run or applier is already active")

// RunExclusive runs fn in ONE transaction holding the projection lock exclusively, so
// no other run or applier can interleave. With truncate the projected state is emptied
// first (rebuild). The run is recorded in projection_run in the same transaction:
// either everything commits or the read model is left exactly as it was.
func (p *Postgres) RunExclusive(ctx context.Context, kind string, truncate bool,
	fn func(tx *Postgres) (read, applied int, err error)) (read, applied int, err error) {
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
			if err := tx.Exec(`TRUNCATE read_model.vehicle_action_history, read_model.inventory_vehicle, read_model.projection_aggregate`).Error; err != nil {
				return err
			}
		}
		var fnErr error
		read, applied, fnErr = fn(&Postgres{
			db:        tx,
			inTx:      true,
			joined:    true,
			exclusive: true,
		})
		if fnErr != nil {
			return fnErr
		}
		return tx.Exec(`INSERT INTO read_model.projection_run(kind, started_at, finished_at, events_read, events_applied)
			VALUES (?, ?, now(), ?, ?)`, kind, started, read, applied).Error
	})
	if err != nil {
		return 0, 0, err
	}
	return read, applied, nil
}
