package repos

import (
	"context"
	"database/sql"
	"fmt"

	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/gorm"
)

type eventRepo struct {
	db        *gorm.DB
	serialize eventsourcing.Serializer
}

func newEventRepo(db *gorm.DB, s eventsourcing.Serializer) *eventRepo {
	return &eventRepo{db: db, serialize: s}
}

func (r *eventRepo) Get(ctx context.Context, aggregateID string, fromVersion, toVersion int, agg eventsourcing.Aggregate) error {
	root := agg.Root()
	if root.AggregateType() == "" {
		return fmt.Errorf("aggregate type is empty")
	}
	if toVersion <= fromVersion {
		return nil
	}

	rows, err := r.db.WithContext(ctx).Raw(`
		SELECT id, aggregate_id, event_type, version, data, metadata, created_at
		FROM es_event
		WHERE aggregate_id = ?
			AND version > ?
			AND version <= ?
		ORDER BY version ASC`, aggregateID, fromVersion, toVersion).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			evt      eventsourcing.Event
			data     string
			metadata sql.NullString
		)
		if err := rows.Scan(&evt.ID, &evt.AggregateID, &evt.EventType, &evt.Version, &data, &metadata, &evt.CreatedAt); err != nil {
			return err
		}
		factory, ok := r.serialize.Type(root.AggregateType(), evt.EventType)
		if !ok {
			return fmt.Errorf("unknown event type %s_%s", root.AggregateType(), evt.EventType)
		}
		eventData := factory()
		if err := r.serialize.Unmarshal([]byte(data), eventData); err != nil {
			return fmt.Errorf("unmarshal event: %w", err)
		}
		evt.Data = eventData
		if metadata.Valid && metadata.String != "" {
			var decoded interface{}
			if err := r.serialize.Unmarshal([]byte(metadata.String), &decoded); err != nil {
				return fmt.Errorf("unmarshal event metadata: %w", err)
			}
			evt.Metadata = decoded
		}
		if err := root.LoadFromHistory(agg, []eventsourcing.Event{evt}); err != nil {
			return fmt.Errorf("apply %s version %d: %w", evt.EventType, evt.Version, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read events: %w", err)
	}
	return nil
}

func (r *eventRepo) Append(ctx context.Context, e eventsourcing.Event) error {
	eData, err := r.serialize.Marshal(e.Data)
	if err != nil {
		return fmt.Errorf("serialize event data: %w", err)
	}
	eMetadata, err := r.serialize.Marshal(e.Metadata)
	if err != nil {
		return fmt.Errorf("serialize event metadata: %w", err)
	}
	err = r.db.WithContext(ctx).Exec(`
		INSERT INTO es_event(aggregate_id, event_type, version, data, metadata, created_at)
		VALUES(?, ?, ?, ?, ?, ?)`,
		e.AggregateID, e.EventType, e.Version, eData, eMetadata, e.CreatedAt).Error
	if err != nil {
		if isDuplicateKey(err) {
			return fmt.Errorf("%w: aggregate %s version %d: %w", ErrDuplicateEvent, e.AggregateID, e.Version, err)
		}
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}
