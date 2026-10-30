package eventsourcing

type Event struct {
	ID          int64       `json:"id"`
	AggregateID string      `json:"aggregate_id"`
	Version     int         `json:"version"`
	EventType   string      `json:"event_type"`
	Data        interface{} `json:"data"`
	Metadata    interface{} `json:"metadata"`

	CreatedAt int64 `json:"created_at"`

	// AggregateType is stored on every event row so that a consumer of a single row
	// (e.g. CDC) can decode it. The event store sets it on save.
	AggregateType string `json:"aggregate_type,omitempty"`
	// SchemaVersion is the payload schema version; 0 means DefaultSchemaVersion.
	SchemaVersion int `json:"schema_version,omitempty"`
}

// DefaultSchemaVersion is the schema version of every payload until an event type
// changes shape; a breaking change bumps it for that type and adds an upcaster on read.
const DefaultSchemaVersion = 1
