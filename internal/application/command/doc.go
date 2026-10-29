// Package command contains the write-side application services: it loads the
// VehicleInventoryAggregate, resolves the dealership business date, runs the domain
// command and saves the resulting events through the existing AggregateStore.
package command
