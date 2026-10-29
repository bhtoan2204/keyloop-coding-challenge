package messaging

import (
	"fmt"
	"time"

	"keyloop-coding-challenge/package/eventsourcing"
)

// ChangeEvent is a decoded change-feed record.
type ChangeEvent struct {
	Event eventsourcing.Event
	// Snapshot is true for records of the connector's initial snapshot.
	Snapshot bool
	// ChangedAt is when MySQL recorded the change (zero when the connector omits it).
	ChangedAt time.Time
}

// Decoder turns Kafka records of the es_event change feed into events.
type Decoder struct {
	serializer eventsourcing.Serializer
}

// NewDecoder decodes payloads with the serializer the event store uses, so the same
// aggregate and event registrations apply.
func NewDecoder(serializer eventsourcing.Serializer) *Decoder {
	return &Decoder{serializer: serializer}
}

// Decode checks the record key against the row: the key decides the partition, so a
// row under another aggregate's key could be applied out of order. The value is read
// first: a truncate event has no key, and should be reported as the operation it is.
func (d *Decoder) Decode(rec Record) (ChangeEvent, error) {
	env, err := parseEnvelope(rec.Value)
	if err != nil {
		return ChangeEvent{}, err
	}
	keyID, err := aggregateIDFromKey(rec.Key)
	if err != nil {
		return ChangeEvent{}, err
	}
	if env.After.AggregateID != keyID {
		return ChangeEvent{}, fmt.Errorf("%w: key aggregate %q but row aggregate %q",
			ErrMalformedRecord, keyID, env.After.AggregateID)
	}
	ev, err := decodeEventRow(d.serializer, env.After)
	if err != nil {
		return ChangeEvent{}, err
	}
	changed := ChangeEvent{
		Event:    ev,
		Snapshot: env.Source.isSnapshot(),
	}
	if env.Source.TsMs > 0 {
		changed.ChangedAt = time.UnixMilli(env.Source.TsMs)
	}
	return changed, nil
}
