package eventstore_test

import (
	"context"
	"reflect"
	"testing"
)

func TestSaveWithMetadataPersistsAndReplays(t *testing.T) {
	db, _, store := newStore(t, 0)
	agg := newQty(t, "inv")
	addQty(t, agg, 1)
	if err := agg.ApplyChangeWithMetadata(agg, &qtyEvent{Delta: 1}, map[string]interface{}{"actor_id": "from-event", "note": "x"}); err != nil {
		t.Fatal(err)
	}
	command := map[string]interface{}{"actor_id": "manager-42", "correlation_id": "req-1"}
	if err := store.SaveWithMetadata(context.Background(), agg, command); err != nil {
		t.Fatal(err)
	}

	loaded := mustLoad(t, store, "inv")
	want := []interface{}{
		map[string]interface{}{"actor_id": "manager-42", "correlation_id": "req-1"},
		// keys the event already carried win over the command metadata
		map[string]interface{}{"actor_id": "from-event", "correlation_id": "req-1", "note": "x"},
	}
	for i, applied := range loaded.applied {
		if !reflect.DeepEqual(applied.meta, want[i]) {
			t.Fatalf("event %d metadata %#v, want %#v", i+1, applied.meta, want[i])
		}
	}
	var raw string
	if err := db.Raw(`SELECT metadata FROM es_event WHERE aggregate_id = ? AND version = 1`, "inv").Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if raw != `{"actor_id":"manager-42","correlation_id":"req-1"}` {
		t.Fatalf("stored metadata %s", raw)
	}
}

func TestSaveWithMetadataDoesNotMutatePendingEvents(t *testing.T) {
	_, _, store := newStore(t, 0)
	first := newQty(t, "inv")
	addQty(t, first, 1)
	mustSave(t, store, first)

	stale := newQty(t, "inv") // conflicts: version 1 already exists
	addQty(t, stale, 1)
	before := stale.CloneEvents()
	err := store.SaveWithMetadata(context.Background(), stale, map[string]interface{}{"actor_id": "a"})
	if err == nil {
		t.Fatal("expected conflict")
	}
	if after := stale.Events(); !reflect.DeepEqual(after, before) {
		t.Fatalf("pending events were modified\nbefore: %#v\n after: %#v", before, after)
	}
}
