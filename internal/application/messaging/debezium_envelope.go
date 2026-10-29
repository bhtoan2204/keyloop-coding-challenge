package messaging

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Debezium change operations. Only inserts (live) and snapshot reads are valid for
// the append-only es_event table.
const (
	opCreate   = "c"
	opSnapshot = "r"
	opUpdate   = "u"
	opDelete   = "d"
	opTruncate = "t"
)

// changeEnvelope is a Debezium change event value produced by the JSON converter with
// schemas disabled: {"before": …, "after": {row}, "source": {…}, "op": "c", "ts_ms": …}.
type changeEnvelope struct {
	After  *eventRow    `json:"after"`
	Source changeSource `json:"source"`
	Op     string       `json:"op"`
}

// changeSource is the part of the Debezium source block the consumer uses.
type changeSource struct {
	// Snapshot is "true", "first", "last" or "last_in_data_collection" for records of
	// the initial snapshot and "false" (or absent) for records read from the binlog.
	Snapshot string `json:"snapshot"`
	// TsMs is when the change was made in MySQL (for snapshot records: when it was read).
	TsMs  int64  `json:"ts_ms"`
	Table string `json:"table"`
}

func (s changeSource) isSnapshot() bool {
	return s.Snapshot != "" && s.Snapshot != "false"
}

// parseEnvelope decodes a change event value and returns it when it carries an
// inserted or snapshotted es_event row.
func parseEnvelope(value []byte) (*changeEnvelope, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		// A tombstone follows a delete; es_event rows are never deleted.
		return nil, fmt.Errorf("%w: empty value (tombstone)", ErrUnexpectedOperation)
	}
	var env changeEnvelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return nil, fmt.Errorf("%w: value is not a JSON change event: %w", ErrMalformedRecord, err)
	}
	switch env.Op {
	case opCreate, opSnapshot:
	case opUpdate, opDelete, opTruncate:
		return nil, fmt.Errorf("%w: op %q", ErrUnexpectedOperation, env.Op)
	default:
		// Also the case for a value with an embedded schema ({"schema":…,"payload":…}):
		// the connector must use value.converter.schemas.enable=false.
		return nil, fmt.Errorf("%w: unknown op %q", ErrMalformedRecord, env.Op)
	}
	if env.Source.Table != "" && env.Source.Table != "es_event" {
		return nil, fmt.Errorf("%w: change of table %q", ErrMalformedRecord, env.Source.Table)
	}
	if env.After == nil {
		return nil, fmt.Errorf("%w: op %q without a row", ErrMalformedRecord, env.Op)
	}
	return &env, nil
}
