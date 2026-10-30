// Package query contains the read-side application services. They read the
// read_model schema (plus the event_store.dealership reference data, never the event
// history) and are independent of the command side. Commands update the read model in
// the same transaction as their events.
package query
