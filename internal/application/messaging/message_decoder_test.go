package messaging_test

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"keyloop-coding-challenge/internal/application/messaging"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
)

func golden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSpace(b)
}

// TestDecodeGoldenDebeziumSnapshotRecord decodes a message captured from the real
// connector (Debezium 3.6.2 on MySQL 8.4, snapshot record).
func TestDecodeGoldenDebeziumSnapshotRecord(t *testing.T) {
	d := messaging.NewDecoder(newSerializer(t))
	got, err := d.Decode(messaging.Record{
		Topic: topic,
		Key:   golden(t, "snapshot_action_proposed.key.json"),
		Value: golden(t, "snapshot_action_proposed.value.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ev := got.Event
	if ev.ID != 2 || ev.AggregateID != "demo-a97d2d22" || ev.Version != 2 || ev.AggregateType != "VehicleInventoryAggregate" ||
		ev.EventType != "VehicleActionProposedEvent" || ev.SchemaVersion != 1 || ev.CreatedAt != 1793257395 {
		t.Fatalf("event header: %+v", ev)
	}
	want := &events.VehicleActionProposedEvent{
		ActionID:   "01a18678-54a0-71d6-9d3b-3cf4842d40c2",
		ActionType: domain.ActionPriceReduction,
		Note:       "Reduce by 5%",
	}
	if !reflect.DeepEqual(ev.Data, want) {
		t.Fatalf("payload: %#v", ev.Data)
	}
	metadata, ok := ev.Metadata.(map[string]interface{})
	if !ok || metadata["actor_id"] != "manager-42" || metadata["correlation_id"] != "01a18678-54a0-7129-b317-106a83a750db" {
		t.Fatalf("metadata: %#v", ev.Metadata)
	}
	if !got.Snapshot || got.ChangedAt.UnixMilli() != 1793265013000 {
		t.Fatalf("snapshot flag / time: %+v", got)
	}
}

// TestDecodeGoldenDebeziumLiveRecord decodes a binlog (streamed) record captured from
// the real connector during the Phase 5 end-to-end run.
func TestDecodeGoldenDebeziumLiveRecord(t *testing.T) {
	d := messaging.NewDecoder(newSerializer(t))
	got, err := d.Decode(messaging.Record{
		Topic: topic,
		Key:   golden(t, "live_action_proposed.key.json"),
		Value: golden(t, "live_action_proposed.value.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ev := got.Event
	if ev.ID != 14 || ev.AggregateID != "e2e-3348a2" || ev.Version != 2 || ev.EventType != "VehicleActionProposedEvent" {
		t.Fatalf("event header: %+v", ev)
	}
	want := &events.VehicleActionProposedEvent{
		ActionID:   "01a18702-a3db-78ae-9e2d-42ce272f016c",
		ActionType: domain.ActionPriceReduction,
		Note:       "5% off",
	}
	if !reflect.DeepEqual(ev.Data, want) {
		t.Fatalf("payload: %#v", ev.Data)
	}
	if got.Snapshot || got.ChangedAt.UnixMilli() != 1793266459664 {
		t.Fatalf("live record flagged snapshot=%v at %v", got.Snapshot, got.ChangedAt)
	}
}

func TestDecodeLiveRecord(t *testing.T) {
	d := messaging.NewDecoder(newSerializer(t))
	e := stream("v1", 10, 1)[0]
	got, err := d.Decode(messaging.Record{
		Key:   e.key(),
		Value: e.changeValue("c", 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Snapshot || got.ChangedAt.UnixMilli() != 1793257395000 {
		t.Fatalf("live record: %+v", got)
	}
	if !reflect.DeepEqual(got.Event.Data, e.data) || got.Event.ID != 10 || got.Event.Version != 1 {
		t.Fatalf("event: %+v", got.Event)
	}
}

// replace is strings.Replace that fails the test when old is absent, so a case cannot
// silently test the valid record.
func replace(t *testing.T, s, old, new string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("%q not in %s", old, s)
	}
	return strings.Replace(s, old, new, 1)
}

func TestDecodeRejectsInvalidRecords(t *testing.T) {
	e := stream("v1", 10, 2)[1]
	valid := string(e.changeValue("c", 1))
	without := func(old string) string { return replace(t, valid, old, "") }
	cases := []struct {
		name  string
		key   string
		value string
		want  error
	}{
		{"no key", "", valid, messaging.ErrMalformedRecord},
		{"key not JSON", "v1", valid, messaging.ErrMalformedRecord},
		{"key without aggregate_id", `{"id":"v1"}`, valid, messaging.ErrMalformedRecord},
		{"key of another aggregate", `{"aggregate_id":"v2"}`, valid, messaging.ErrMalformedRecord},
		{"tombstone", string(e.key()), "", messaging.ErrUnexpectedOperation},
		{"null value", string(e.key()), "null", messaging.ErrUnexpectedOperation},
		{"update", string(e.key()), string(e.changeValue("u", 1)), messaging.ErrUnexpectedOperation},
		{"delete", string(e.key()), string(e.changeValue("d", 1)), messaging.ErrUnexpectedOperation},
		{"truncate", string(e.key()), string(e.changeValue("t", 1)), messaging.ErrUnexpectedOperation},
		{"truncate without key", "", `{"before":null,"after":null,"source":{"table":"es_event"},"op":"t"}`, messaging.ErrUnexpectedOperation},
		{"value with embedded schema", string(e.key()), `{"schema":{},"payload":` + valid + `}`, messaging.ErrMalformedRecord},
		{"not JSON", string(e.key()), "{", messaging.ErrMalformedRecord},
		{"other table", string(e.key()), replace(t, valid, `"es_event"`, `"es_aggregate"`), messaging.ErrMalformedRecord},
		{"create without row", string(e.key()), `{"op":"c","after":null}`, messaging.ErrMalformedRecord},
		{"missing version", string(e.key()), without(`,"version":2`), messaging.ErrMalformedRecord},
		{"missing data", string(e.key()), replace(t, valid, `"data":`, `"payload":`), messaging.ErrMalformedRecord},
		{"missing aggregate type", string(e.key()), without(`"aggregate_type":"VehicleInventoryAggregate",`), messaging.ErrMalformedRecord},
		{"payload not JSON", string(e.key()), replace(t, valid, `"data":"{`, `"data":"x{`), messaging.ErrMalformedRecord},
		{"unknown event type", string(e.key()), replace(t, valid, "VehicleActionProposedEvent", "VehicleRepaintedEvent"), messaging.ErrUnknownEventType},
		{"unknown aggregate type", string(e.key()), replace(t, valid, `"aggregate_type":"VehicleInventoryAggregate"`, `"aggregate_type":"Customer"`), messaging.ErrUnknownEventType},
		{"newer schema version", string(e.key()), string(e.changeValue("c", 2)), messaging.ErrUnsupportedSchemaVersion},
		{"missing schema version", string(e.key()), without(`"schema_version":1,`), messaging.ErrMalformedRecord},
	}
	d := messaging.NewDecoder(newSerializer(t))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.Decode(messaging.Record{
				Key:   []byte(tc.key),
				Value: []byte(tc.value),
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDecodeAcceptsNullMetadata(t *testing.T) {
	e := stream("v1", 10, 1)[0]
	value := replace(t, string(e.changeValue("c", 1)), `"metadata":"{\"actor_id\":\"manager-42\",\"correlation_id\":\"req-1\"}"`, `"metadata":null`)
	got, err := messaging.NewDecoder(newSerializer(t)).Decode(messaging.Record{
		Key:   e.key(),
		Value: []byte(value),
	})
	if err != nil || got.Event.Metadata != nil {
		t.Fatalf("got %+v, %v", got.Event, err)
	}
}
