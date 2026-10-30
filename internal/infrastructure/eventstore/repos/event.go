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
		FROM event_store.es_event
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
		// Append requires a type and migration 2 backfilled older rows; an empty type can
		// only remain on a legacy row whose es_aggregate row is missing, and is tolerated.
		if evt.AggregateType != "" && evt.AggregateType != root.AggregateType() {
			return fmt.Errorf("%w: event %d of aggregate %s has type %s, expected %s",
				ErrAggregateTypeMismatch, evt.ID, aggregateID, evt.AggregateType, root.AggregateType())
		}
		if err := r.decode(&evt, root.AggregateType(), data, metadata); err != nil {
			return err
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

// Append inserts one event and returns its database-generated id. The id is read back
// with RETURNING inside the caller's transaction: it is never derived from a sequence
// value, and ids say nothing about commit order across transactions.
func (r *eventRepo) Append(ctx context.Context, e eventsourcing.Event) (int64, error) {
	// Every row must be decodable on its own (rebuilds read rows without the aggregate).
	if e.AggregateType == "" {
		return 0, fmt.Errorf("append event: aggregate %s version %d: aggregate type is required", e.AggregateID, e.Version)
	}
	eData, err := r.serialize.Marshal(e.Data)
	if err != nil {
		return 0, fmt.Errorf("serialize event data: %w", err)
	}
	eMetadata, err := r.serialize.Marshal(e.Metadata)
	if err != nil {
		return 0, fmt.Errorf("serialize event metadata: %w", err)
	}
	schemaVersion := e.SchemaVersion
	if schemaVersion == 0 {
		schemaVersion = eventsourcing.DefaultSchemaVersion
	}
	var id int64
	err = r.db.WithContext(ctx).Raw(`
		INSERT INTO event_store.es_event(aggregate_id, aggregate_type, event_type, version, schema_version, data, metadata, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id`,
		e.AggregateID, e.AggregateType, e.EventType, e.Version, schemaVersion, eData, eMetadata, e.CreatedAt).
		Scan(&id).Error
	if err != nil {
		if isDuplicateKey(err) {
			return 0, fmt.Errorf("%w: aggregate %s version %d: %w", ErrDuplicateEvent, e.AggregateID, e.Version, err)
		}
		return 0, fmt.Errorf("append event: %w", err)
	}
	return id, nil
}

// decode fills evt.Data and evt.Metadata from the stored JSON, using the serializer's
// registration for aggregateType. Unknown types and schema versions are errors.
func (r *eventRepo) decode(evt *eventsourcing.Event, aggregateType, data string, metadata sql.NullString) error {
	// No upcasters exist yet: every payload is version 1. When an event type changes
	// shape, upcast older versions here instead of rejecting them.
	if evt.SchemaVersion != eventsourcing.DefaultSchemaVersion {
		return fmt.Errorf("%w: %s version %d of aggregate %s has schema version %d",
			ErrUnsupportedSchemaVersion, evt.EventType, evt.Version, evt.AggregateID, evt.SchemaVersion)
	}
	factory, ok := r.serialize.Type(aggregateType, evt.EventType)
	if !ok {
		return fmt.Errorf("%w %s_%s", ErrUnknownEventType, aggregateType, evt.EventType)
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
	return nil
}
