package eventstore_test

import (
	"context"
	"strings"
	"testing"

	"keyloop-coding-challenge/package/eventsourcing"
)

func TestSaveRollsBackWhenALaterEventFails(t *testing.T) {
	db, serializer, _ := newStore(t, 0)
	if err := serializer.RegisterAggregate(&brittleAggregate{}); err != nil {
		t.Fatal(err)
	}
	store := newStoreWith(t, db, serializer, 0)

	agg := &brittleAggregate{}
	if err := agg.SetID("brittle"); err != nil {
		t.Fatal(err)
	}
	if err := agg.ApplyChange(agg, &qtyEvent{Delta: 1}); err != nil {
		t.Fatal(err)
	}
	// The second event cannot be serialised, so its INSERT never happens after the
	// first event's INSERT already ran inside the transaction.
	if err := agg.ApplyChange(agg, &unencodableEvent{C: make(chan int)}); err != nil {
		t.Fatal(err)
	}
	// The failure must come from the second event, after the first one's INSERT.
	if err := store.Save(context.Background(), agg); err == nil || !strings.Contains(err.Error(), "unsupported type: chan int") {
		t.Fatalf("expected the second event's serialisation to fail the save, got %v", err)
	}
	if v := eventVersions(t, db, "brittle"); len(v) != 0 {
		t.Fatalf("partial events persisted: %v", v)
	}
	var aggregates int64
	if err := db.Raw(`SELECT COUNT(*) FROM event_store.es_aggregate WHERE id = ?`, "brittle").Scan(&aggregates).Error; err != nil {
		t.Fatal(err)
	}
	if aggregates != 0 {
		t.Fatal("es_aggregate row persisted")
	}
	if len(agg.Events()) != 2 || agg.BaseVersion() != 0 {
		t.Fatalf("pending=%d base=%d", len(agg.Events()), agg.BaseVersion())
	}
}

// brittleAggregate accepts an event that cannot be JSON-encoded, to inject a failure
// in the middle of a multi-event append.
type brittleAggregate struct {
	eventsourcing.AggregateRoot
}

type unencodableEvent struct {
	C chan int
}

func (a *brittleAggregate) RegisterEvents(register eventsourcing.RegisterEventsFunc) error {
	return register(&qtyEvent{}, &unencodableEvent{})
}

func (a *brittleAggregate) Transition(eventsourcing.Event) error { return nil }
