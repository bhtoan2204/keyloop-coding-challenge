package eventsourcing

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

type eventFunc = func() interface{}

type RegisterEventsFunc = func(events ...interface{}) error

var _ Serializer = (*serializer)(nil)

type Serializer interface {
	RegisterAggregate(agg BaseAggregate) error
	Type(typ, name string) (eventFunc, bool)
	Marshal(v interface{}) ([]byte, error)
	Unmarshal(data []byte, v interface{}) error
}

type serializer struct {
	eventRegister map[string]eventFunc
}

func NewSerializer() Serializer {
	return &serializer{eventRegister: make(map[string]eventFunc)}
}

func (s *serializer) RegisterAggregate(agg BaseAggregate) error {
	name, err := aggregateName(agg)
	if err != nil {
		return err
	}
	return agg.RegisterEvents(func(events ...interface{}) error {
		for _, item := range events {
			factory, err := newEventFunc(item)
			if err != nil {
				return err
			}
			eventName := reflect.TypeOf(factory()).Elem().Name()
			key := name + "_" + eventName
			if _, ok := s.eventRegister[key]; ok {
				return fmt.Errorf("event %s is already registered", key)
			}
			s.eventRegister[key] = factory
		}
		return nil
	})
}

func (s *serializer) Type(typ, name string) (eventFunc, bool) {
	f, ok := s.eventRegister[typ+"_"+name]
	return f, ok
}

func (s *serializer) Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func (s *serializer) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

func aggregateName(agg BaseAggregate) (string, error) {
	if agg == nil {
		return "", errors.New("aggregate is nil")
	}
	value := reflect.ValueOf(agg)
	typ := value.Type()
	if typ.Kind() != reflect.Ptr || value.IsNil() || typ.Elem().Kind() != reflect.Struct || typ.Elem().Name() == "" {
		return "", errors.New("aggregate must be a non-nil pointer to a named struct")
	}
	return typ.Elem().Name(), nil
}

func newEventFunc(event interface{}) (eventFunc, error) {
	typ := reflect.TypeOf(event)
	if typ == nil || typ.Kind() != reflect.Ptr || reflect.ValueOf(event).IsNil() || typ.Elem().Name() == "" {
		return nil, errors.New("event must be a non-nil pointer to a named type")
	}
	elem := typ.Elem()
	return func() interface{} {
		return reflect.New(elem).Interface()
	}, nil
}
