package eventsourcing

import "testing"

type sampleEvent struct {
	Name string `json:"name"`
}

type sampleAggregate struct {
	AggregateRoot
}

func (a *sampleAggregate) RegisterEvents(register RegisterEventsFunc) error {
	return register(&sampleEvent{})
}

type plainAggregate struct{}

func (plainAggregate) RegisterEvents(RegisterEventsFunc) error { return nil }

type valueEventAggregate struct{}

func (a *valueEventAggregate) RegisterEvents(register RegisterEventsFunc) error {
	return register(sampleEvent{})
}

func TestRegisterAggregate(t *testing.T) {
	serializer := NewSerializer()
	if err := serializer.RegisterAggregate((*sampleAggregate)(nil)); err == nil {
		t.Fatal("expected nil aggregate error")
	}
	if err := serializer.RegisterAggregate(plainAggregate{}); err == nil {
		t.Fatal("expected non-pointer aggregate error")
	}
	if err := serializer.RegisterAggregate(&valueEventAggregate{}); err == nil {
		t.Fatal("expected non-pointer event error")
	}
	if err := serializer.RegisterAggregate(&sampleAggregate{}); err != nil {
		t.Fatal(err)
	}
	if err := serializer.RegisterAggregate(&sampleAggregate{}); err == nil {
		t.Fatal("expected duplicate registration")
	}

	factory, ok := serializer.Type("sampleAggregate", "sampleEvent")
	if !ok {
		t.Fatal("event was not registered")
	}
	raw, err := serializer.Marshal(&sampleEvent{Name: "vin"})
	if err != nil {
		t.Fatal(err)
	}
	got := factory()
	if err := serializer.Unmarshal(raw, got); err != nil {
		t.Fatal(err)
	}
	event, ok := got.(*sampleEvent)
	if !ok || event.Name != "vin" {
		t.Fatalf("round trip: %#v", got)
	}
}
