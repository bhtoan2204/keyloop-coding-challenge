package eventstore_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
)

func TestReadStreamReturnsOneAggregateVersionRange(t *testing.T) {
	db, serializer, store := newStore(t, 0)
	for _, id := range []string{"a", "b"} {
		agg := newQty(t, id)
		for i := 0; i < 4; i++ {
			addQty(t, agg, i)
			mustSave(t, store, agg)
		}
	}
	events := repos.New(db, serializer).EventStore()

	got, err := events.ReadStream(context.Background(), "b", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	var versions []string
	for _, ev := range got {
		versions = append(versions, fmt.Sprintf("%s/%d", ev.AggregateID, ev.Version))
	}
	if !reflect.DeepEqual(versions, []string{"b/2", "b/3"}) {
		t.Fatalf("read %v", versions)
	}
	if ev := got[1]; ev.AggregateType != "qtyAggregate" || ev.SchemaVersion != 1 || ev.ID == 0 ||
		!reflect.DeepEqual(ev.Data, &qtyEvent{Delta: 2}) {
		t.Fatalf("decoded event %+v", ev)
	}

	for _, r := range [][2]int{{3, 3}, {4, 9}, {2, 1}} {
		got, err := events.ReadStream(context.Background(), "b", r[0], r[1])
		if err != nil || len(got) != 0 {
			t.Fatalf("ReadStream(b, %d, %d) = %d events, %v", r[0], r[1], len(got), err)
		}
	}
	if got, err := events.ReadStream(context.Background(), "missing", 0, 10); err != nil || len(got) != 0 {
		t.Fatalf("unknown aggregate: %d events, %v", len(got), err)
	}
}

func TestReadStreamRejectsUndecodableRows(t *testing.T) {
	db, serializer, store := newStore(t, 0)
	agg := newQty(t, "a")
	addQty(t, agg, 1)
	mustSave(t, store, agg)
	if err := db.Exec(`UPDATE event_store.es_event SET schema_version = 2`).Error; err != nil {
		t.Fatal(err)
	}
	_, err := repos.New(db, serializer).EventStore().ReadStream(context.Background(), "a", 0, 1)
	if err == nil {
		t.Fatal("a row of an unknown schema version was decoded")
	}
}
