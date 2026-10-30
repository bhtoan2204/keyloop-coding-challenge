package eventsourcing

import (
	"errors"
	"testing"
)

type stubEvent struct {
	N int
}

type stubAggregate struct {
	AggregateRoot
	failOn int
	got    []int
}

func (a *stubAggregate) RegisterEvents(register RegisterEventsFunc) error {
	return register(&stubEvent{})
}

func (a *stubAggregate) Transition(e Event) error {
	event, ok := e.Data.(*stubEvent)
	if !ok {
		return errors.New("unknown event")
	}
	if a.failOn == e.Version {
		return errors.New("apply failed")
	}
	a.got = append(a.got, event.N)
	return nil
}

func TestPendingEventVersions(t *testing.T) {
	agg := &stubAggregate{}
	if err := agg.SetID("inv-1"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if err := agg.ApplyChange(agg, &stubEvent{N: i}); err != nil {
			t.Fatal(err)
		}
	}
	events := agg.Events()
	if len(events) != 3 {
		t.Fatalf("pending events: got %d", len(events))
	}
	for i, event := range events {
		if event.Version != i+1 {
			t.Fatalf("event %d version: got %d", i, event.Version)
		}
		if event.EventType != "stubEvent" {
			t.Fatalf("event type: got %s", event.EventType)
		}
	}
	if agg.Version() != 3 || agg.BaseVersion() != 0 {
		t.Fatalf("version=%d base=%d", agg.Version(), agg.BaseVersion())
	}
}

func TestRejectedEventIsNotPending(t *testing.T) {
	agg := &stubAggregate{failOn: 1}
	if err := agg.SetID("inv-1"); err != nil {
		t.Fatal(err)
	}
	if err := agg.ApplyChange(agg, &stubEvent{N: 1}); err == nil {
		t.Fatal("expected apply error")
	}
	if agg.IsUnsaved() || agg.Version() != 0 || len(agg.got) != 0 {
		t.Fatalf("rejected event changed aggregate: version=%d unsaved=%v got=%v", agg.Version(), agg.IsUnsaved(), agg.got)
	}
	agg.failOn = 0
	if err := agg.ApplyChange(agg, &stubEvent{N: 1}); err != nil {
		t.Fatal(err)
	}
	if agg.Events()[0].Version != 1 {
		t.Fatalf("version after rejected apply: got %d", agg.Events()[0].Version)
	}
}

func TestUpdateClearsPendingEvents(t *testing.T) {
	agg := &stubAggregate{}
	if err := agg.SetID("inv-1"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if err := agg.ApplyChange(agg, &stubEvent{N: i}); err != nil {
			t.Fatal(err)
		}
	}
	agg.Update()
	if agg.IsUnsaved() || agg.Version() != 3 || agg.BaseVersion() != 3 {
		t.Fatalf("after update version=%d base=%d unsaved=%v", agg.Version(), agg.BaseVersion(), agg.IsUnsaved())
	}
	if err := agg.ApplyChange(agg, &stubEvent{N: 4}); err != nil {
		t.Fatal(err)
	}
	if agg.Events()[0].Version != 4 {
		t.Fatalf("next version: got %d", agg.Events()[0].Version)
	}
}

func TestLoadFromHistory(t *testing.T) {
	t.Run("replays in order without pending events", func(t *testing.T) {
		agg := &stubAggregate{}
		events := []Event{
			{AggregateID: "inv-1", Version: 1, Data: &stubEvent{N: 1}},
			{AggregateID: "inv-1", Version: 2, Data: &stubEvent{N: 2}},
		}
		if err := agg.LoadFromHistory(agg, events); err != nil {
			t.Fatal(err)
		}
		if agg.Version() != 2 || agg.BaseVersion() != 2 || agg.IsUnsaved() {
			t.Fatalf("version=%d base=%d unsaved=%v", agg.Version(), agg.BaseVersion(), agg.IsUnsaved())
		}
		if len(agg.got) != 2 || agg.got[0] != 1 || agg.got[1] != 2 {
			t.Fatalf("applied %#v", agg.got)
		}
	})

	t.Run("gap stops before the bad event", func(t *testing.T) {
		agg := &stubAggregate{}
		err := agg.LoadFromHistory(agg, []Event{
			{AggregateID: "inv-1", Version: 1, Data: &stubEvent{N: 1}},
			{AggregateID: "inv-1", Version: 3, Data: &stubEvent{N: 3}},
		})
		if err == nil {
			t.Fatal("expected gap error")
		}
		if agg.BaseVersion() != 1 || len(agg.got) != 1 {
			t.Fatalf("base=%d applied=%v err=%v", agg.BaseVersion(), agg.got, err)
		}
	})

	t.Run("apply error does not advance version", func(t *testing.T) {
		agg := &stubAggregate{failOn: 1}
		err := agg.LoadFromHistory(agg, []Event{{AggregateID: "inv-1", Version: 1, Data: &stubEvent{N: 1}}})
		if err == nil {
			t.Fatal("expected apply error")
		}
		if agg.BaseVersion() != 0 || agg.Version() != 0 || len(agg.got) != 0 {
			t.Fatalf("base=%d version=%d applied=%v", agg.BaseVersion(), agg.Version(), agg.got)
		}
	})

	t.Run("aggregate id mismatch", func(t *testing.T) {
		agg := &stubAggregate{}
		if err := agg.SetID("inv-1"); err != nil {
			t.Fatal(err)
		}
		err := agg.LoadFromHistory(agg, []Event{{AggregateID: "other", Version: 1, Data: &stubEvent{N: 1}}})
		if err == nil {
			t.Fatal("expected id mismatch")
		}
		if agg.BaseVersion() != 0 {
			t.Fatalf("base=%d", agg.BaseVersion())
		}
	})
}

func TestSetID(t *testing.T) {
	agg := &stubAggregate{}
	if err := agg.SetID(""); err != ErrIDEmpty {
		t.Fatalf("empty id: %v", err)
	}
	if err := agg.SetID("inv-1"); err != nil {
		t.Fatal(err)
	}
	if err := agg.SetID("inv-1"); err != nil {
		t.Fatal(err)
	}
	if err := agg.SetID("inv-2"); err != ErrAggExisted {
		t.Fatalf("replace id: %v", err)
	}
	if agg.AggregateID() != "inv-1" {
		t.Fatalf("id: got %s", agg.AggregateID())
	}
}
