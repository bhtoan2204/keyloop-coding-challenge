package repos

import (
	"context"
	"fmt"

	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/gorm"
)

type eventRepo struct {
	db        *gorm.DB
	serialize eventsourcing.Serializer
}

func newEventRepo(db *gorm.DB, s eventsourcing.Serializer) *eventRepo {
	return &eventRepo{
		db:        db,
		serialize: s,
	}
}

func (r *eventRepo) Get(ctx context.Context, aggregateID string, fromVersion, toVersion int, agg eventsourcing.Aggregate) error {
	root := agg.Root()

	rows, err := r.db.WithContext(ctx).Raw(`
			SELECT id, aggregate_id, event_type, version, data
			FROM es_event
			WHERE aggregate_id = ?
				AND (? = 0 OR version > ?)
				AND (? = 0 OR version <= ?)
			ORDER BY version ASC`, aggregateID, fromVersion, fromVersion, toVersion, toVersion).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			evt  eventsourcing.Event
			data string
		)
		if err := rows.Scan(&evt.ID, &evt.AggregateID, &evt.EventType, &evt.Version, &data); err != nil {
			return err
		}

		f, ok := r.serialize.Type(root.AggregateType(), evt.EventType)
		if !ok {
			return fmt.Errorf("cant serialize event with type: %s_%s", root.AggregateType(), evt.EventType)
		}

		eventData := f()

		err := r.serialize.Unmarshal([]byte(data), &eventData)
		if err != nil {
			return fmt.Errorf("unmarshal event failed with err=%w", err)
		}

		evt.Data = eventData
		root.LoadFromHistory(agg, []eventsourcing.Event{evt})
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("Get event rows.err err=%w", err)
	}

	return nil
}

func (r *eventRepo) Append(ctx context.Context, e eventsourcing.Event) error {
	eData, err := r.serialize.Marshal(e.Data)
	if err != nil {
		return fmt.Errorf("serilize e.Data err=%w", err)
	}

	eMetadata, err := r.serialize.Marshal(e.Metadata)
	if err != nil {
		return fmt.Errorf("serilize e.Data err=%w", err)
	}

	err = r.db.WithContext(ctx).Exec(`
		INSERT INTO es_event(aggregate_id, event_type, version, data, metadata, created_at)
		VALUES(?, ?, ?, ?, ?, ?)`,
		e.AggregateID, e.EventType, e.Version, eData, eMetadata, e.CreatedAt).Error
	if err != nil {
		return fmt.Errorf("append event err=%w", err)
	}

	return nil
}
