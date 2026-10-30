package repos

import (
	"keyloop-coding-challenge/pkg/eventsourcing"

	"gorm.io/gorm"
)

var _ Repos = (*repos)(nil)

type Repos interface {
	EventStore() EventStore
}

type repos struct {
	ev EventStore
}

func New(db *gorm.DB, s eventsourcing.Serializer) Repos {
	ev := NewEventStore(db, s)

	return &repos{
		ev: ev,
	}
}

func (r *repos) EventStore() EventStore {
	return r.ev
}
