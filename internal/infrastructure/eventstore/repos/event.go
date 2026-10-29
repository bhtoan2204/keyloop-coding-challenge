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
		return fmt.Errorf("unknown event type %s_%s", aggregateType, evt.EventType)
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
	var batch []eventsourcing.Event
	for rows.Next() {
		var (
			evt      eventsourcing.Event
			data     string
			metadata sql.NullString
		)
		if err := rows.Scan(&evt.ID, &evt.AggregateID, &evt.AggregateType, &evt.EventType, &evt.Version,
			&evt.SchemaVersion, &data, &metadata, &evt.CreatedAt); err != nil {
			rows.Close()
			return 0, err
		}
		if evt.AggregateType == "" {
			rows.Close()
			return 0, fmt.Errorf("read all: event %d of aggregate %s has no aggregate type", evt.ID, evt.AggregateID)
		}
		if err := r.decode(&evt, evt.AggregateType, data, metadata); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, evt)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("read all: %w", err)
	}
	rows.Close()
	for _, evt := range batch {
		if err := fn(evt); err != nil {
			return 0, err
		}
	}
	return len(batch), nil
}
