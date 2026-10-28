package eventsourcing

type Event struct {
	ID          int64       `json:"id"`
	AggregateID string      `json:"aggregate_id"`
	Version     int         `json:"version"`
	EventType   string      `json:"event_type"`
	Data        interface{} `json:"data"`
	Metadata    interface{} `json:"metadata"`

	CreatedAt int64 `json:"created_at"`
}
