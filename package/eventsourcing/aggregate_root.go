package eventsourcing

import (
	"errors"
	"fmt"
	"reflect"
	"time"
)

var (
	ErrAggExisted = errors.New("aggregate already has an ID")
	ErrIDEmpty    = errors.New("aggregate id is empty")
)

// AggregateRoot tracks persisted version and events waiting to be saved.
// version and baseVersion are the last version committed to the store.
// Update clears pending events after that commit succeeds.
type AggregateRoot struct {
	aggregateID   string
	aggregateType string
	version       int
	baseVersion   int
	events        []Event
}

func (ar *AggregateRoot) SetID(id string) error {
	if id == "" {
		return ErrIDEmpty
	}
	if ar.aggregateID == id {
		return nil
	}
	if ar.aggregateID != "" {
		return ErrAggExisted
	}
	ar.aggregateID = id
	return nil
}

func (ar *AggregateRoot) SetAggregateType(typ string) {
	ar.aggregateType = typ
}

func (ar *AggregateRoot) AggregateID() string {
	return ar.aggregateID
}

func (ar *AggregateRoot) AggregateType() string {
	return ar.aggregateType
}

func (ar *AggregateRoot) Root() *AggregateRoot {
	return ar
}

// Version includes pending events. BaseVersion is the last persisted version.
func (ar *AggregateRoot) Version() int {
	if len(ar.events) > 0 {
		return ar.events[len(ar.events)-1].Version
	}
	return ar.version
}

func (ar *AggregateRoot) BaseVersion() int {
	return ar.baseVersion
}

func (ar *AggregateRoot) Events() []Event {
	return ar.events
}

func (ar *AggregateRoot) CloneEvents() []Event {
	evs := make([]Event, len(ar.events))
	copy(evs, ar.events)
	return evs
}

func (ar *AggregateRoot) IsUnsaved() bool {
	return len(ar.events) > 0
}

func (ar *AggregateRoot) ApplyChange(agg Aggregate, data interface{}) error {
	return ar.ApplyChangeWithMetadata(agg, data, nil)
}

func (ar *AggregateRoot) ApplyChangeWithMetadata(agg Aggregate, data interface{}, metadata map[string]interface{}) error {
	if ar.aggregateID == "" {
		return fmt.Errorf("missing aggregate id, aggregate_type=%s", ar.aggregateType)
	}
	eventType, err := eventTypeName(data)
	if err != nil {
		return err
	}
	event := Event{
		AggregateID: ar.aggregateID,
		Version:     ar.nextVersion(),
		EventType:   eventType,
		CreatedAt:   time.Now().Unix(),
		Data:        data,
		Metadata:    metadata,
	}
	if err := agg.Transition(event); err != nil {
		return err
	}
	ar.events = append(ar.events, event)
	return nil
}

// LoadFromHistory replays committed events. baseVersion is the version already applied,
// including a restored snapshot, so the next event must be baseVersion+1.
func (ar *AggregateRoot) LoadFromHistory(agg Aggregate, events []Event) error {
	for _, e := range events {
		if ar.aggregateID == "" {
			ar.aggregateID = e.AggregateID
		} else if e.AggregateID != ar.aggregateID {
			return fmt.Errorf("event aggregate id %s, expected %s", e.AggregateID, ar.aggregateID)
		}
		if e.Version != ar.baseVersion+1 {
			return fmt.Errorf("event version %d, expected %d", e.Version, ar.baseVersion+1)
		}
		if err := agg.Transition(e); err != nil {
			return err
		}
		ar.version = e.Version
		ar.baseVersion = e.Version
	}
	return nil
}

func (ar *AggregateRoot) Update() {
	if len(ar.events) == 0 {
		return
	}
	last := ar.events[len(ar.events)-1]
	ar.version = last.Version
	ar.baseVersion = last.Version
	ar.events = nil
}

func (ar *AggregateRoot) SetInternal(id string, baseVersion, version int) {
	ar.aggregateID = id
	ar.baseVersion = baseVersion
	ar.version = version
	ar.events = nil
}

func (ar *AggregateRoot) nextVersion() int {
	if n := len(ar.events); n > 0 {
		return ar.events[n-1].Version + 1
	}
	return ar.version + 1
}

func eventTypeName(data interface{}) (string, error) {
	typ := reflect.TypeOf(data)
	if typ == nil || typ.Kind() != reflect.Ptr || typ.Elem().Name() == "" || reflect.ValueOf(data).IsNil() {
		return "", errors.New("event data must be a non-nil pointer to a named type")
	}
	return typ.Elem().Name(), nil
}
