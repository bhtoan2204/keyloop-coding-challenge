---
paths:
  - "package/eventsourcing/**"
  - "internal/domain/**"
  - "internal/infrastructure/**"
  - "internal/application/**"
---
# Event Sourcing & CQRS rules

1. **Append-only history.** `es_event` rows are never updated or deleted by application code.
   Corrections are new events. (Tests may tamper with SQLite rows to simulate corruption.)
2. **Atomic persistence.** All events of one command, the `es_aggregate` CAS and any snapshot are
   written in one `WithTransaction`. Pending events are cleared (`Update()`) only after commit.
3. **Optimistic concurrency.** Keep the CAS on `es_aggregate.version` plus the unique
   `(aggregate_id, version)`. On `ErrConcurrencyConflict` re-load and re-run the command
   (bounded retries); never re-insert the same events. Map MySQL 1213/1205 only if a test shows them.
4. **Contiguous versions.** Versions run 1..n per aggregate. `LoadFromHistory` rejects gaps; the
   projector halts on gaps rather than skipping.
5. **State changes only in apply methods.** Command methods validate and call `ApplyChange`; the
   `applyX` methods validate the event against current state *before* mutating, and must be
   deterministic: no clock, no I/O, no randomness. Replay must equal command execution.
6. **Typed payloads.** Pointer structs with JSON tags in `internal/domain/events`. No
   `map[string]any` payloads. Never store derived values (`is_aging`, `days_in_stock`, age).
7. **Schema compatibility.** Stored type names are Go struct names and are frozen. Only add
   payload fields with zero-value-safe defaults; never rename or retype a field. Breaking changes
   need a schema version and an upcaster on read. Snapshot JSON field names are stored data too.
8. **Snapshot correctness.** An aggregate with unexported state must implement
   `MarshalJSON`/`UnmarshalJSON`; every state field must round-trip (test compares the whole
   struct). Never trust a snapshot that was not covered by such a test.
9. **Idempotent projections.** Apply an event only if `version == stored + 1`; skip
   `version <= stored`; halt on `version > stored + 1`. State and version commit in one Postgres tx.
10. **Explicit consistency.** Write side: strongly consistent per aggregate. Read side: eventually
    consistent, monotonic per aggregate. Say which side an API response comes from. Aging is
    computed at query time from `stocked_on` and the dealership business date.
11. **CDC.** Debezium captures `es_event` only, with Kafka key `aggregate_id`. Delivery is
    at-least-once; never assume exactly-once across MySQL → Kafka → Postgres. Rebuilds read
    MySQL, never Kafka retention.
