package viewstore

import (
	"context"

	"gorm.io/gorm"
)

var _ ViewStore = (*Postgres)(nil)

// Postgres implements ViewStore (projection writes) and query.ReadModel (reads) on
// PostgreSQL through GORM with raw SQL, the same access style as the event store.
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

func (p *Postgres) WithTransaction(ctx context.Context, fn func(tx ViewStore) error) error {
	if p.joined {
		return fn(p)
	}
	return p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Postgres{db: tx, inTx: true})
	})
}
