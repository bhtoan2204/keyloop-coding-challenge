package eventstore_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/package/eventsourcing"
)

func TestReadAllStreamsHistoryInAggregateVersionOrder(t *testing.T) {
	db, serializer, store := newStore(t, 0)
	// Interleave saves so insertion order (es_event.id) differs from stream order.
	aggs := map[string]*qtyAggregate{}
	for round := 0; round < 3; round++ {
		for _, id := range []string{"c", "a", "b"} {
			if aggs[id] == nil {
				aggs[id] = newQty(t, id)
			}
			if err := aggs[id].ApplyChangeWithMetadata(aggs[id], &qtyEvent{Delta: round}, map[string]interface{}{"round": float64(round)}); err != nil {
				t.Fatal(err)
			}
			mustSave(t, store, aggs[id])
		}
	}

	var got []string
	var seen []eventsourcing.Event
	err := repos.New(db, serializer).EventStore().ReadAll(context.Background(), 2, func(ev eventsourcing.Event) error {
		got = append(got, fmt.Sprintf("%s/%d", ev.AggregateID, ev.Version))
		seen = append(seen, ev)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a/1", "a/2", "a/3", "b/1", "b/2", "b/3", "c/1", "c/2", "c/3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order %v", got)
	}
	ev := seen[4] // b/2
	if ev.AggregateType != "qtyAggregate" || ev.SchemaVersion != 1 || ev.ID == 0 || ev.CreatedAt == 0 ||
		!reflect.DeepEqual(ev.Data, &qtyEvent{Delta: 1}) || !reflect.DeepEqual(ev.Metadata, map[string]interface{}{"round": 1.0}) {
		t.Fatalf("decoded event %+v", ev)
	}
}

func TestReadAllStopsOnCallbackError(t *testing.T) {
	db, serializer, store := newStore(t, 0)
	agg := newQty(t, "a")
	addQty(t, agg, 1)
	addQty(t, agg, 1)
	mustSave(t, store, agg)
	stop := fmt.Errorf("stop")
	calls := 0
	err := repos.New(db, serializer).EventStore().ReadAll(context.Background(), 1, func(eventsourcing.Event) error {
		calls++
		return stop
	})
	if err != stop || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestReadAllRejectsUndecodableRows(t *testing.T) {
	db, serializer, store := newStore(t, 0)
	agg := newQty(t, "a")
	addQty(t, agg, 1)
	mustSave(t, store, agg)
	mustExec(t, db, `UPDATE es_event SET schema_version = 2 WHERE aggregate_id = 'a'`)
	err := repos.New(db, serializer).EventStore().ReadAll(context.Background(), 10, func(eventsourcing.Event) error { return nil })
	if err == nil {
		t.Fatal("expected an error for an unsupported schema version")
	}
}
