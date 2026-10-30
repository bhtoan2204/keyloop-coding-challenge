package repos

import (
	"context"
	"database/sql"
	"fmt"

	"gorm.io/gorm"

	"keyloop-coding-challenge/pkg/eventsourcing"
)

// ReadAll streams every stored event to fn, ordered by (aggregate_id, version), in
// keyset batches on the unique (aggregate_id, version) index.
//
// It runs on the caller's connection and transaction and does not start its own: the
// rebuild calls it inside its run transaction while holding the projection lock
// exclusively. Every command takes that lock (shared) before it touches the event
// store and keeps it until commit, so no event can commit while the run reads, and the
// batches see one stable history even under READ COMMITTED. Ordering never relies on
// es_event.id: ids are allocated at insert, not at commit.
func (r *eventRepo) ReadAll(ctx context.Context, batchSize int, fn func(eventsourcing.Event) error) error {
	if batchSize <= 0 {
		batchSize = 500
	}
	afterID, afterVersion := "", 0
	for {
		n, err := r.readBatch(ctx, r.db, afterID, afterVersion, batchSize, func(evt eventsourcing.Event) error {
			afterID, afterVersion = evt.AggregateID, evt.Version
			return fn(evt)
		})
		if err != nil {
			return err
		}
		if n < batchSize {
			return nil
		}
	}
}

func (r *eventRepo) readBatch(ctx context.Context, conn *gorm.DB, afterID string, afterVersion, limit int,
	fn func(eventsourcing.Event) error) (int, error) {
	rows, err := conn.WithContext(ctx).Raw(`
		SELECT id, aggregate_id, aggregate_type, event_type, version, schema_version, data, metadata, created_at
		FROM event_store.es_event
		WHERE (aggregate_id, version) > (?, ?)
		ORDER BY aggregate_id, version
		LIMIT ?`, afterID, afterVersion, limit).Rows()
	if err != nil {
		return 0, fmt.Errorf("read all: %w", err)
	}
	// Decode the whole batch before calling fn, so the result set is closed while fn
	// runs (fn may be slow: it writes to the read model).
	batch, err := r.scanEvents(rows)
	if err != nil {
		return 0, fmt.Errorf("read all: %w", err)
	}
	for _, evt := range batch {
		if err := fn(evt); err != nil {
			return 0, err
		}
	}
	return len(batch), nil
}

func (r *eventRepo) ReadStream(ctx context.Context, aggregateID string, afterVersion, toVersion int) ([]eventsourcing.Event, error) {
	if toVersion <= afterVersion {
		return nil, nil
	}
	rows, err := r.db.WithContext(ctx).Raw(`
		SELECT id, aggregate_id, aggregate_type, event_type, version, schema_version, data, metadata, created_at
		FROM event_store.es_event
		WHERE aggregate_id = ?
			AND version > ?
			AND version <= ?
		ORDER BY version`, aggregateID, afterVersion, toVersion).Rows()
	if err != nil {
		return nil, fmt.Errorf("read stream %s: %w", aggregateID, err)
	}
	events, err := r.scanEvents(rows)
	if err != nil {
		return nil, fmt.Errorf("read stream %s: %w", aggregateID, err)
	}
	return events, nil
}

// scanEvents decodes and closes rows of the es_event columns selected by ReadAll and
// ReadStream. Every row must carry its aggregate type: it is decoded on its own.
func (r *eventRepo) scanEvents(rows *sql.Rows) ([]eventsourcing.Event, error) {
	defer rows.Close()
	var events []eventsourcing.Event
	for rows.Next() {
		var (
			evt      eventsourcing.Event
			data     string
			metadata sql.NullString
		)
		if err := rows.Scan(&evt.ID, &evt.AggregateID, &evt.AggregateType, &evt.EventType, &evt.Version,
			&evt.SchemaVersion, &data, &metadata, &evt.CreatedAt); err != nil {
			return nil, err
		}
		if evt.AggregateType == "" {
			return nil, fmt.Errorf("event %d of aggregate %s has no aggregate type", evt.ID, evt.AggregateID)
		}
		if err := r.decode(&evt, evt.AggregateType, data, metadata); err != nil {
			return nil, err
		}
		events = append(events, evt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}
