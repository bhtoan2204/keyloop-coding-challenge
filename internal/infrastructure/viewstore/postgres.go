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
	// exclusive is set inside RunExclusive: the run holds the projection lock
	// exclusively, so no other applier can run and per-aggregate locks are not needed.
	exclusive bool
}

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
