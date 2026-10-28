package viewstore

import (
	"context"
	"keyloop-coding-challenge/package/eventsourcing"
)

type ViewStore interface {
	Save(ctx context.Context, ev eventsourcing.Event) error
}
