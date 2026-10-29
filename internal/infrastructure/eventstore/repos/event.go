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
		SELECT id, aggregate_id, aggregate_type, event_type, version, schema_version, data, metadata, created_at
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
		if err := rows.Scan(&evt.ID, &evt.AggregateID, &evt.AggregateType, &evt.EventType, &evt.Version,
			&evt.SchemaVersion, &data, &metadata, &evt.CreatedAt); err != nil {
			return err
		}
		// Rows written before migration 2 are backfilled; an empty type is tolerated
		// for rows appended directly through the repository without one.
		if evt.AggregateType != "" && evt.AggregateType != root.AggregateType() {
			return fmt.Errorf("%w: event %d of aggregate %s has type %s, expected %s",
				ErrAggregateTypeMismatch, evt.ID, aggregateID, evt.AggregateType, root.AggregateType())
		}
		// No upcasters exist yet: every payload is version 1. When an event type changes
		// shape, upcast older versions here instead of rejecting them.
		if evt.SchemaVersion != eventsourcing.DefaultSchemaVersion {
			return fmt.Errorf("%w: %s version %d of aggregate %s has schema version %d",
				ErrUnsupportedSchemaVersion, evt.EventType, evt.Version, aggregateID, evt.SchemaVersion)
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
	// Every row must be decodable on its own (CDC), and the migration backfill only ran
	// once, so a row without a type would stay undecodable.
	if e.AggregateType == "" {
		return fmt.Errorf("append event: aggregate %s version %d: aggregate type is required", e.AggregateID, e.Version)
	}
	eData, err := r.serialize.Marshal(e.Data)
	if err != nil {
		return fmt.Errorf("serialize event data: %w", err)
	}
	eMetadata, err := r.serialize.Marshal(e.Metadata)
	if err != nil {
		return fmt.Errorf("serialize event metadata: %w", err)
	}
	schemaVersion := e.SchemaVersion
	if schemaVersion == 0 {
		schemaVersion = eventsourcing.DefaultSchemaVersion
	}
	err = r.db.WithContext(ctx).Exec(`
		INSERT INTO es_event(aggregate_id, aggregate_type, event_type, version, schema_version, data, metadata, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		e.AggregateID, e.AggregateType, e.EventType, e.Version, schemaVersion, eData, eMetadata, e.CreatedAt).Error
	if err != nil {
		if isDuplicateKey(err) {
			return fmt.Errorf("%w: aggregate %s version %d: %w", ErrDuplicateEvent, e.AggregateID, e.Version, err)
		}
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}
