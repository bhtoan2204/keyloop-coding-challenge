// Package viewstore is the PostgreSQL read model. It composes the repositories into the
// application ports: Postgres implements projection.Store (the write side of
// projections, bound to one transaction) and ReadModel implements query.ReadModel.
package viewstore

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/application/projection"
	"keyloop-coding-challenge/internal/infrastructure/viewstore/repositories"
)

var _ projection.Store = (*Postgres)(nil)

// ErrNotInTransaction: the write methods need a transaction-bound store (the aggregate
// lock is transaction-scoped).
var ErrNotInTransaction = errors.New("viewstore: write outside WithTransaction")

// Postgres is the projection store. Every repository it hands out is built on its one
// *gorm.DB, so all writes of a transaction-bound store share that transaction.
type Postgres struct {
	db   *gorm.DB
	inTx bool
	// joined is set for a store bound to a caller's transaction (a command's save
	// transaction, or a run): WithTransaction joins it instead of opening a savepoint,
	// so everything commits or rolls back with that one transaction.
	joined bool
	// exclusive is set inside RunExclusive: the run holds the projection lock
	// exclusively, so no other applier can run and per-aggregate locks are not needed.
	exclusive bool
}

func NewPostgres(db *gorm.DB) *Postgres {
	return &Postgres{db: db}
}

// InTransaction binds the store to a transaction the caller owns (and commits).
func InTransaction(tx *gorm.DB) *Postgres {
	return &Postgres{
		db:     tx,
		inTx:   true,
		joined: true,
	}
}

// Tx is the transaction handle of a store bound to one (nil otherwise).
func (p *Postgres) Tx() *gorm.DB {
	if !p.inTx {
		return nil
	}
	return p.db
}

func (p *Postgres) WithTransaction(ctx context.Context, fn func(tx projection.Store) error) error {
	if p.joined {
		return fn(p)
	}
	return p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Postgres{db: tx, inTx: true})
	})
}

// LockProjectionShared takes the projection lock in shared mode until the transaction
// ends. Commands take it before any event-store lock; a rebuild takes it exclusively.
func (p *Postgres) LockProjectionShared(ctx context.Context) error {
	if !p.inTx {
		return ErrNotInTransaction
	}
	return p.projections().LockShared(ctx)
}

func (p *Postgres) LockAggregate(ctx context.Context, aggregateID string) (int, error) {
	if !p.inTx {
		return 0, ErrNotInTransaction
	}
	if !p.exclusive {
		if err := p.projections().LockAggregate(ctx, aggregateID); err != nil {
			return 0, err
		}
	}
	return p.projections().AppliedVersion(ctx, aggregateID)
}

func (p *Postgres) SetAppliedVersion(ctx context.Context, aggregateID string, version int, eventID int64) error {
	if !p.inTx {
		return ErrNotInTransaction
	}
	return p.projections().SetAppliedVersion(ctx, aggregateID, version, eventID)
}

func (p *Postgres) Vehicle(ctx context.Context, vehicleID string) (*projection.ProjectedVehicle, error) {
	return p.vehicles().Projected(ctx, vehicleID)
}

func (p *Postgres) SaveVehicle(ctx context.Context, v projection.ProjectedVehicle) error {
	if !p.inTx {
		return ErrNotInTransaction
	}
	return p.vehicles().Save(ctx, v)
}

func (p *Postgres) AppendHistory(ctx context.Context, e projection.ActionHistoryEntry) error {
	if !p.inTx {
		return ErrNotInTransaction
	}
	return p.history().Append(ctx, e)
}

func (p *Postgres) projections() repositories.ProjectionRepository {
	return repositories.NewProjectionRepository(p.db)
}

func (p *Postgres) vehicles() repositories.VehicleRepository {
	return repositories.NewVehicleRepository(p.db)
}

func (p *Postgres) history() repositories.ActionHistoryRepository {
	return repositories.NewActionHistoryRepository(p.db)
}
