package eventstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/pkg/eventsourcing"

	"gorm.io/gorm"
)

type qtyEvent struct {
	Delta int `json:"delta"`
}

type appliedEvent struct {
	version int
	at      int64
	meta    interface{}
}

type qtyAggregate struct {
	eventsourcing.AggregateRoot
	qty      int
	failSnap bool
	applied  []appliedEvent
}

func (a *qtyAggregate) RegisterEvents(register eventsourcing.RegisterEventsFunc) error {
	return register(&qtyEvent{})
}

func (a *qtyAggregate) Transition(e eventsourcing.Event) error {
	event, ok := e.Data.(*qtyEvent)
	if !ok {
		return errors.New("unknown event")
	}
	a.qty += event.Delta
	a.applied = append(a.applied, appliedEvent{version: e.Version, at: e.CreatedAt, meta: e.Metadata})
	return nil
}

func (a *qtyAggregate) MarshalJSON() ([]byte, error) {
	if a.failSnap {
		return nil, errors.New("snapshot failed")
	}
	return json.Marshal(struct {
		Qty int `json:"qty"`
	}{Qty: a.qty})
}

func (a *qtyAggregate) UnmarshalJSON(data []byte) error {
	var state struct {
		Qty int `json:"qty"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	a.qty = state.Qty
	return nil
}

type otherAggregate struct {
	eventsourcing.AggregateRoot
}

func (a *otherAggregate) RegisterEvents(eventsourcing.RegisterEventsFunc) error { return nil }

func (a *otherAggregate) Transition(e eventsourcing.Event) error {
	if _, ok := e.Data.(*qtyEvent); !ok {
		return errors.New("unknown event")
	}
	return nil
}

func TestSaveMultipleEventsAndSaveAgain(t *testing.T) {
	db, _, store := newStore(t, 10)
	agg := newQty(t, "inv")
	addQty(t, agg, 1)
	addQty(t, agg, 1)
	addQty(t, agg, 1)
	for i, event := range agg.Events() {
		if event.Version != i+1 {
			t.Fatalf("pending version %d: got %d", i, event.Version)
		}
	}

	mustSave(t, store, agg)
	if agg.IsUnsaved() || agg.Version() != 3 || agg.BaseVersion() != 3 {
		t.Fatalf("after save version=%d base=%d unsaved=%v", agg.Version(), agg.BaseVersion(), agg.IsUnsaved())
	}
	if versions := eventVersions(t, db, "inv"); !reflect.DeepEqual(versions, []int{1, 2, 3}) {
		t.Fatalf("versions: %v", versions)
	}

	mustSave(t, store, agg)
	if versions := eventVersions(t, db, "inv"); !reflect.DeepEqual(versions, []int{1, 2, 3}) {
		t.Fatalf("versions after repeated save: %v", versions)
	}

	addQty(t, agg, 1)
	mustSave(t, store, agg)
	loaded := mustLoad(t, store, "inv")
	if loaded.qty != 4 || loaded.Version() != 4 {
		t.Fatalf("loaded qty=%d version=%d", loaded.qty, loaded.Version())
	}
	if versions := eventVersions(t, db, "inv"); !reflect.DeepEqual(versions, []int{1, 2, 3, 4}) {
		t.Fatalf("versions: %v", versions)
	}
}

func TestTransactionRollback(t *testing.T) {
	db, serializer, store := newStore(t, 1)
	agg := newQty(t, "inv")
	agg.failSnap = true
	addQty(t, agg, 1)

	err := store.Save(context.Background(), agg)
	if err == nil {
		t.Fatal("expected snapshot error")
	}
	if !agg.IsUnsaved() || agg.BaseVersion() != 0 || agg.qty != 1 {
		t.Fatalf("pending state base=%d unsaved=%v qty=%d", agg.BaseVersion(), agg.IsUnsaved(), agg.qty)
	}
	_, _, findErr := repos.New(db, serializer).EventStore().FindAggregate(context.Background(), "inv")
	if !errors.Is(findErr, eventstore.ErrAggregateNotFound) {
		t.Fatalf("aggregate after rollback: %v", findErr)
	}
	if versions := eventVersions(t, db, "inv"); len(versions) != 0 {
		t.Fatalf("events after rollback: %v", versions)
	}

	agg.failSnap = false
	mustSave(t, store, agg)
	loaded := mustLoad(t, store, "inv")
	if loaded.qty != 1 || loaded.Version() != 1 {
		t.Fatalf("retry qty=%d version=%d", loaded.qty, loaded.Version())
	}
}

func TestOptimisticConcurrency(t *testing.T) {
	db, _, store := newStore(t, 10)
	first := newQty(t, "inv")
	addQty(t, first, 1)
	mustSave(t, store, first)

	second := mustLoad(t, store, "inv")
	addQty(t, first, 1)
	addQty(t, second, 1)
	mustSave(t, store, first)

	err := store.Save(context.Background(), second)
	if !errors.Is(err, eventstore.ErrConcurrencyConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	if !second.IsUnsaved() || second.BaseVersion() != 1 {
		t.Fatalf("loser base=%d unsaved=%v", second.BaseVersion(), second.IsUnsaved())
	}
	if versions := eventVersions(t, db, "inv"); !reflect.DeepEqual(versions, []int{1, 2}) {
		t.Fatalf("versions: %v", versions)
	}
}

func TestDatabaseErrorIsNotConcurrencyConflict(t *testing.T) {
	db, _, store := newStore(t, 10)
	agg := newQty(t, "inv")
	addQty(t, agg, 1)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	err = store.Save(context.Background(), agg)
	if err == nil || errors.Is(err, eventstore.ErrConcurrencyConflict) || retryable(err) {
		t.Fatalf("got %v", err)
	}
	if !agg.IsUnsaved() {
		t.Fatal("pending events were cleared")
	}
}

func TestReplayErrors(t *testing.T) {
	t.Run("unknown event type", func(t *testing.T) {
		db, _, store := newStore(t, 10)
		agg := newQty(t, "inv")
		addQty(t, agg, 1)
		mustSave(t, store, agg)
		if err := db.Exec(`UPDATE event_store.es_event SET event_type = ? WHERE aggregate_id = ?`, "MissingEvent", "inv").Error; err != nil {
			t.Fatal(err)
		}
		err := store.Get(context.Background(), "inv", &qtyAggregate{})
		if err == nil || !strings.Contains(err.Error(), "unknown event type") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("version gap", func(t *testing.T) {
		db, _, store := newStore(t, 10)
		agg := newQty(t, "inv")
		addQty(t, agg, 1)
		mustSave(t, store, agg)
		if err := db.Exec(`UPDATE event_store.es_aggregate SET version = 3 WHERE id = ?`, "inv").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO event_store.es_event(aggregate_id, aggregate_type, event_type, version, data, metadata, created_at) VALUES(?, ?, ?, ?, ?, ?, ?)`,
			"inv", "qtyAggregate", "qtyEvent", 3, `{"delta":1}`, "null", 1).Error; err != nil {
			t.Fatal(err)
		}
		err := store.Get(context.Background(), "inv", &qtyAggregate{})
		if err == nil || !strings.Contains(err.Error(), "expected") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("stream shorter than aggregate version", func(t *testing.T) {
		db, _, store := newStore(t, 10)
		agg := newQty(t, "inv")
		addQty(t, agg, 1)
		mustSave(t, store, agg)
		if err := db.Exec(`UPDATE event_store.es_aggregate SET version = 3 WHERE id = ?`, "inv").Error; err != nil {
			t.Fatal(err)
		}
		err := store.Get(context.Background(), "inv", &qtyAggregate{})
		if err == nil || !strings.Contains(err.Error(), "stored version") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestSnapshotRestoreAndLaterEvents(t *testing.T) {
	db, _, store := newStore(t, 2)
	agg := newQty(t, "inv")
	addQty(t, agg, 1)
	addQty(t, agg, 1)
	mustSave(t, store, agg)
	if data := snapshotData(t, db, "inv", 2); !strings.Contains(data, `"qty":2`) {
		t.Fatalf("snapshot data: %s", data)
	}

	addQty(t, agg, 1)
	mustSave(t, store, agg)
	if versions := snapshotVersions(t, db, "inv"); !reflect.DeepEqual(versions, []int{2}) {
		t.Fatalf("snapshots: %v", versions)
	}
	deleteEventsUpTo(t, db, "inv", 2)

	loaded := mustLoad(t, store, "inv")
	if loaded.qty != 3 || loaded.Version() != 3 {
		t.Fatalf("qty=%d version=%d", loaded.qty, loaded.Version())
	}
	if len(loaded.applied) != 1 || loaded.applied[0].version != 3 {
		t.Fatalf("replayed %#v", loaded.applied)
	}
}

func TestSnapshotIntervalChange(t *testing.T) {
	db, serializer, store := newStore(t, 2)
	agg := newQty(t, "batch")
	for i := 0; i < 4; i++ {
		addQty(t, agg, 1)
	}
	mustSave(t, store, agg)
	if versions := snapshotVersions(t, db, "batch"); !reflect.DeepEqual(versions, []int{4}) {
		t.Fatalf("snapshots after crossed boundaries: %v", versions)
	}
	if loaded := mustLoad(t, store, "batch"); loaded.qty != 4 {
		t.Fatalf("batch qty=%d", loaded.qty)
	}

	agg = newQty(t, "inv")
	addQty(t, agg, 1)
	addQty(t, agg, 1)
	mustSave(t, store, agg)
	addQty(t, agg, 1)
	mustSave(t, store, agg)

	wide := eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(db, serializer), 10)
	loaded := mustLoad(t, wide, "inv")
	if loaded.qty != 3 {
		t.Fatalf("qty with old snapshot: %d", loaded.qty)
	}
	addQty(t, loaded, 1)
	mustSave(t, wide, loaded)
	if versions := snapshotVersions(t, db, "inv"); !reflect.DeepEqual(versions, []int{2}) {
		t.Fatalf("snapshots after wider interval: %v", versions)
	}

	deleteEventsUpTo(t, db, "inv", 2)
	restored := mustLoad(t, wide, "inv")
	if restored.qty != 4 || restored.Version() != 4 {
		t.Fatalf("restored qty=%d version=%d", restored.qty, restored.Version())
	}

	tight := eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(db, serializer), 1)
	addQty(t, restored, 1)
	mustSave(t, tight, restored)
	if versions := snapshotVersions(t, db, "inv"); !reflect.DeepEqual(versions, []int{2, 5}) {
		t.Fatalf("snapshots after tighter interval: %v", versions)
	}
	if err := db.Exec(`DELETE FROM event_store.es_event WHERE aggregate_id = ?`, "inv").Error; err != nil {
		t.Fatal(err)
	}
	final := mustLoad(t, tight, "inv")
	if final.qty != 5 || final.Version() != 5 {
		t.Fatalf("final qty=%d version=%d", final.qty, final.Version())
	}
}

func TestMetadataRoundTrip(t *testing.T) {
	_, _, store := newStore(t, 10)
	agg := newQty(t, "inv")
	metadata := map[string]interface{}{"source": "desk"}
	if err := agg.ApplyChangeWithMetadata(agg, &qtyEvent{Delta: 2}, metadata); err != nil {
		t.Fatal(err)
	}
	createdAt := agg.Events()[0].CreatedAt
	mustSave(t, store, agg)

	loaded := mustLoad(t, store, "inv")
	if loaded.qty != 2 || len(loaded.applied) != 1 {
		t.Fatalf("qty=%d applied=%d", loaded.qty, len(loaded.applied))
	}
	got := loaded.applied[0]
	if got.at != createdAt || got.version != 1 {
		t.Fatalf("applied %#v, created %d", got, createdAt)
	}
	decoded, ok := got.meta.(map[string]interface{})
	if !ok || decoded["source"] != "desk" {
		t.Fatalf("metadata %#v", got.meta)
	}
}

func TestLoadGuards(t *testing.T) {
	db, _, store := newStore(t, 10)
	if err := store.Get(context.Background(), "", &qtyAggregate{}); !errors.Is(err, eventsourcing.ErrIDEmpty) {
		t.Fatalf("empty id: %v", err)
	}
	if err := store.Get(context.Background(), "missing", &qtyAggregate{}); !errors.Is(err, eventstore.ErrAggregateNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := store.Save(context.Background(), nil); !errors.Is(err, eventstore.ErrInvalidAggregate) {
		t.Fatalf("nil save: %v", err)
	}

	agg := newQty(t, "inv")
	addQty(t, agg, 1)
	mustSave(t, store, agg)
	if err := store.Get(context.Background(), "inv", agg); !errors.Is(err, eventstore.ErrInvalidAggregate) {
		t.Fatalf("dirty load: %v", err)
	}
	if err := store.Get(context.Background(), "inv", &otherAggregate{}); !errors.Is(err, eventstore.ErrAggregateTypeMismatch) {
		t.Fatalf("load type: %v", err)
	}

	other := &otherAggregate{}
	if err := other.SetID("inv"); err != nil {
		t.Fatal(err)
	}
	if err := other.ApplyChange(other, &qtyEvent{Delta: 1}); err != nil {
		t.Fatal(err)
	}
	err := store.Save(context.Background(), other)
	if !errors.Is(err, eventstore.ErrAggregateTypeMismatch) {
		t.Fatalf("save type: %v", err)
	}
	if !other.IsUnsaved() {
		t.Fatal("mismatched save cleared pending events")
	}
	if versions := eventVersions(t, db, "inv"); !reflect.DeepEqual(versions, []int{1}) {
		t.Fatalf("versions: %v", versions)
	}
}

func TestDuplicateEventVersion(t *testing.T) {
	db, serializer, store := newStore(t, 10)
	agg := newQty(t, "inv")
	addQty(t, agg, 1)
	mustSave(t, store, agg)

	es := repos.New(db, serializer).EventStore()
	duplicate := eventsourcing.Event{AggregateID: "inv", AggregateType: "qtyAggregate", Version: 1, EventType: "qtyEvent", Data: &qtyEvent{Delta: 9}, CreatedAt: 1}
	if _, err := es.Append(context.Background(), duplicate); !errors.Is(err, eventstore.ErrDuplicateEvent) {
		t.Fatalf("duplicate: %v", err)
	}

	err := es.WithTransaction(context.Background(), func(tx repos.EventStore) error {
		next := eventsourcing.Event{AggregateID: "inv", AggregateType: "qtyAggregate", Version: 2, EventType: "qtyEvent", Data: &qtyEvent{Delta: 1}, CreatedAt: 1}
		if _, err := tx.Append(context.Background(), next); err != nil {
			return err
		}
		_, err := tx.Append(context.Background(), duplicate)
		return err
	})
	if !errors.Is(err, eventstore.ErrDuplicateEvent) {
		t.Fatalf("duplicate in transaction: %v", err)
	}
	if versions := eventVersions(t, db, "inv"); !reflect.DeepEqual(versions, []int{1}) {
		t.Fatalf("versions: %v", versions)
	}
}

func newStore(t *testing.T, interval int) (*gorm.DB, eventsourcing.Serializer, eventstore.AggregateStore) {
	t.Helper()
	db := openDB(t)
	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&qtyAggregate{}); err != nil {
		t.Fatal(err)
	}
	store := eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(db, serializer), interval)
	return db, serializer, store
}

func newQty(t *testing.T, id string) *qtyAggregate {
	t.Helper()
	agg := &qtyAggregate{}
	if err := agg.SetID(id); err != nil {
		t.Fatal(err)
	}
	return agg
}

func addQty(t *testing.T, agg *qtyAggregate, delta int) {
	t.Helper()
	if err := agg.ApplyChange(agg, &qtyEvent{Delta: delta}); err != nil {
		t.Fatal(err)
	}
}

func mustSave(t *testing.T, store eventstore.AggregateStore, agg eventsourcing.Aggregate) {
	t.Helper()
	if err := store.Save(context.Background(), agg); err != nil {
		t.Fatal(err)
	}
}

func mustLoad(t *testing.T, store eventstore.AggregateStore, id string) *qtyAggregate {
	t.Helper()
	agg := &qtyAggregate{}
	if err := store.Get(context.Background(), id, agg); err != nil {
		t.Fatal(err)
	}
	return agg
}

func eventVersions(t *testing.T, db *gorm.DB, id string) []int {
	t.Helper()
	var rows []struct{ Version int }
	if err := db.Raw(`SELECT version FROM event_store.es_event WHERE aggregate_id = ? ORDER BY version ASC`, id).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	versions := make([]int, len(rows))
	for i, row := range rows {
		versions[i] = row.Version
	}
	return versions
}

func snapshotVersions(t *testing.T, db *gorm.DB, id string) []int {
	t.Helper()
	var rows []struct{ Version int }
	if err := db.Raw(`SELECT version FROM event_store.es_aggregate_snapshot WHERE aggregate_id = ? ORDER BY version ASC`, id).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	versions := make([]int, len(rows))
	for i, row := range rows {
		versions[i] = row.Version
	}
	return versions
}

func snapshotData(t *testing.T, db *gorm.DB, id string, version int) string {
	t.Helper()
	var row struct{ Data string }
	if err := db.Raw(`SELECT data FROM event_store.es_aggregate_snapshot WHERE aggregate_id = ? AND version = ?`, id, version).Scan(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row.Data
}

func deleteEventsUpTo(t *testing.T, db *gorm.DB, id string, version int) {
	t.Helper()
	if err := db.Exec(`DELETE FROM event_store.es_event WHERE aggregate_id = ? AND version <= ?`, id, version).Error; err != nil {
		t.Fatal(err)
	}
}
