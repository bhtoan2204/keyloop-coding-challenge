package eventsourcing

type BaseAggregate interface {
	RegisterEvents(RegisterEventsFunc) error
}

type Aggregate interface {
	BaseAggregate
	Root() *AggregateRoot
	Transition(e Event) error
}
