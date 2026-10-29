package messaging

import (
	"encoding/json"
	"fmt"
)

// recordKey is the Kafka key Debezium writes for an es_event row: the column named in
// message.key.columns (deploy/debezium/es-event-connector.json), serialised by the
// JSON converter without schema. Keying by aggregate_id is what puts every event of
// one aggregate on one partition, in commit order.
type recordKey struct {
	AggregateID *string `json:"aggregate_id"`
}

// aggregateIDFromKey returns the aggregate ID carried in the record key.
func aggregateIDFromKey(key []byte) (string, error) {
	if len(key) == 0 {
		return "", fmt.Errorf("%w: record has no key", ErrMalformedRecord)
	}
	var k recordKey
	if err := json.Unmarshal(key, &k); err != nil {
		return "", fmt.Errorf("%w: key is not JSON: %w", ErrMalformedRecord, err)
	}
	if k.AggregateID == nil || *k.AggregateID == "" {
		return "", fmt.Errorf("%w: key has no aggregate_id", ErrMalformedRecord)
	}
	return *k.AggregateID, nil
}
