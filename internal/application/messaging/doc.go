// Package messaging is the live projection consumer: it reads the change feed of the
// event store (Debezium change events of es_event rows, delivered through Kafka),
// decodes each record into an eventsourcing.Event and applies it to the read model
// through the existing projection.
//
// Delivery is at-least-once. Correctness rests on the projection's per-aggregate
// version check (duplicates are skipped, gaps are never skipped) and on committing a
// partition's offset only after the record's read-model transaction has committed.
// The transport (Kafka) and the projection are ports, implemented in infrastructure.
package messaging
