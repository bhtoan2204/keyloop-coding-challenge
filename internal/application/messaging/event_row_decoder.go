package messaging

import (
	"fmt"

	"keyloop-coding-challenge/package/eventsourcing"
)

// eventRow is an es_event row as Debezium serialises it (one JSON field per column).
// Pointers distinguish a missing column from a zero value.
type eventRow struct {
	ID            *int64  `json:"id"`
	AggregateID   string  `json:"aggregate_id"`
	AggregateType string  `json:"aggregate_type"`
	EventType     string  `json:"event_type"`
	Version       *int    `json:"version"`
	SchemaVersion *int    `json:"schema_version"`
	Data          *string `json:"data"`
	Metadata      *string `json:"metadata"`
	CreatedAt     *int64  `json:"created_at"`
}

// decodeEventRow turns a row into an eventsourcing.Event with a typed payload, using
// the same serializer registrations and rules as the event store's own reader
// (repos: unknown types and schema versions are errors, never guesses).
func decodeEventRow(serializer eventsourcing.Serializer, row *eventRow) (eventsourcing.Event, error) {
	if err := row.validate(); err != nil {
		return eventsourcing.Event{}, err
	}
	ev := eventsourcing.Event{
		ID:            *row.ID,
		AggregateID:   row.AggregateID,
		AggregateType: row.AggregateType,
		Version:       *row.Version,
		EventType:     row.EventType,
		SchemaVersion: *row.SchemaVersion,
		CreatedAt:     *row.CreatedAt,
	}
	// No upcasters exist yet: every payload is version 1.
	if ev.SchemaVersion != eventsourcing.DefaultSchemaVersion {
		return eventsourcing.Event{}, fmt.Errorf("%w: %s v%d (%s) has schema version %d",
			ErrUnsupportedSchemaVersion, ev.AggregateID, ev.Version, ev.EventType, ev.SchemaVersion)
	}
	factory, ok := serializer.Type(ev.AggregateType, ev.EventType)
	if !ok {
		return eventsourcing.Event{}, fmt.Errorf("%w: %s_%s (%s v%d)",
			ErrUnknownEventType, ev.AggregateType, ev.EventType, ev.AggregateID, ev.Version)
	}
	data := factory()
	if err := serializer.Unmarshal([]byte(*row.Data), data); err != nil {
		return eventsourcing.Event{}, fmt.Errorf("%w: payload of %s v%d: %w", ErrMalformedRecord, ev.AggregateID, ev.Version, err)
	}
	ev.Data = data
	if row.Metadata != nil && *row.Metadata != "" {
		var metadata interface{}
		if err := serializer.Unmarshal([]byte(*row.Metadata), &metadata); err != nil {
			return eventsourcing.Event{}, fmt.Errorf("%w: metadata of %s v%d: %w", ErrMalformedRecord, ev.AggregateID, ev.Version, err)
		}
		ev.Metadata = metadata
	}
	return ev, nil
}

func (r *eventRow) validate() error {
	missing := ""
	switch {
	case r.ID == nil:
		missing = "id"
	case r.AggregateID == "":
		missing = "aggregate_id"
	case r.AggregateType == "":
		missing = "aggregate_type"
	case r.EventType == "":
		missing = "event_type"
	case r.Version == nil || *r.Version < 1:
		missing = "version"
	case r.SchemaVersion == nil:
		missing = "schema_version"
	case r.Data == nil:
		missing = "data"
	case r.CreatedAt == nil:
		missing = "created_at"
	}
	if missing != "" {
		return fmt.Errorf("%w: es_event row without %s", ErrMalformedRecord, missing)
	}
	return nil
}
