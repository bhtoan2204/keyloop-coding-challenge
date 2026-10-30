package eventstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/package/eventsourcing"
)

func TestGetRejectsUnsupportedSchemaVersion(t *testing.T) {
	db, _, store := newStore(t, 0)
	agg := newQty(t, "inv")
	addQty(t, agg, 1)
	addQty(t, agg, 1)
	mustSave(t, store, agg)
	// A row as a newer release would write it after reshaping the payload.
	mustExec(t, db, `UPDATE event_store.es_event SET schema_version = 2 WHERE aggregate_id = ? AND version = 2`, "inv")

	err := store.Get(context.Background(), "inv", &qtyAggregate{})
	if !errors.Is(err, eventstore.ErrUnsupportedSchemaVersion) {
		t.Fatalf("got %v", err)
	}
}

func TestGetRejectsEventOfAnotherAggregateType(t *testing.T) {
	db, _, store := newStore(t, 0)
	agg := newQty(t, "inv")
	addQty(t, agg, 1)
	mustSave(t, store, agg)
	mustExec(t, db, `UPDATE event_store.es_event SET aggregate_type = 'otherAggregate' WHERE aggregate_id = ?`, "inv")

	err := store.Get(context.Background(), "inv", &qtyAggregate{})
	if !errors.Is(err, eventstore.ErrAggregateTypeMismatch) {
		t.Fatalf("got %v", err)
	}
}

func TestAppendRequiresAggregateType(t *testing.T) {
	db, serializer, _ := newStore(t, 0)
	es := repos.New(db, serializer).EventStore()
	untyped := eventsourcing.Event{AggregateID: "inv", Version: 1, EventType: "qtyEvent", Data: &qtyEvent{Delta: 1}, CreatedAt: 1}
	_, err := es.Append(context.Background(), untyped)
	if err == nil || !strings.Contains(err.Error(), "aggregate type is required") {
		t.Fatalf("got %v", err)
	}
	if v := eventVersions(t, db, "inv"); len(v) != 0 {
		t.Fatalf("row written: %v", v)
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	db := openDB(t) // migrated to the latest known version
	mustExec(t, db, `INSERT INTO event_store.schema_migrations(version, name) VALUES(99, 'from a newer release')`)
	err := repos.Migrate(db)
	if err == nil || !strings.Contains(err.Error(), "schema version 99") {
		t.Fatalf("got %v", err)
	}
}
