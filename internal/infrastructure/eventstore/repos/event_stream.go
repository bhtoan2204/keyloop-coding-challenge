package repos

import (
	"context"
	"database/sql"
	"fmt"

	"gorm.io/gorm"

	"keyloop-coding-challenge/package/eventsourcing"
)

// ReadAll streams every stored event to fn, ordered by (aggregate_id, version), all
// from ONE consistent snapshot of the event store (MySQL: START TRANSACTION WITH
// CONSISTENT SNAPSHOT, READ ONLY).
//
// Why a snapshot and not an es_event.id cursor: AUTO_INCREMENT ids are assigned at
// insert time, not commit time, so "id > last seen" across separate reads can skip a
// transaction that committed late. Within one snapshot every committed event is
// visible, and because writers of one aggregate are serialised by the version CAS,
// the snapshot always holds a contiguous prefix 1..n of each stream.
func (r *eventRepo) ReadAll(ctx context.Context, batchSize int, fn func(eventsourcing.Event) error) error {
	if batchSize <= 0 {
		batchSize = 500
	}
	return r.db.WithContext(ctx).Connection(func(conn *gorm.DB) error {
		begin := "BEGIN"
		if conn.Dialector.Name() == "mysql" {
			begin = "START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY"
		}
		if err := conn.Exec(begin).Error; err != nil {
			return fmt.Errorf("read all: begin snapshot: %w", err)
		}
		// Roll back even if ctx was cancelled; otherwise the pooled connection would be
		// returned still inside the read-only snapshot transaction.
		defer conn.WithContext(context.WithoutCancel(ctx)).Exec("ROLLBACK")

		afterID, afterVersion := "", 0
		for {
			n, err := r.readBatch(ctx, conn, afterID, afterVersion, batchSize, func(evt eventsourcing.Event) error {
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
	})
}

func (r *eventRepo) readBatch(ctx context.Context, conn *gorm.DB, afterID string, afterVersion, limit int,
	fn func(eventsourcing.Event) error) (int, error) {
	rows, err := conn.WithContext(ctx).Raw(`
		SELECT id, aggregate_id, aggregate_type, event_type, version, schema_version, data, metadata, created_at
		FROM es_event
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
		FROM es_event
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
