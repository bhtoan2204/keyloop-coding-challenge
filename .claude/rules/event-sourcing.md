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
2. **Atomic persistence.** All events of one command, the `es_aggregate` CAS, the read-model
   update (`Projection.Apply` through `CommandProjection`) and any snapshot are written in one
   `WithTransaction`, without savepoints. Pending events are cleared (`Update()`) only after commit.
   A save nested in an outer transaction is refused (`ErrNestedSave`).
3. **Optimistic concurrency.** Keep the CAS on `es_aggregate.version` plus the unique
   `(aggregate_id, version)`. On `ErrConcurrencyConflict` re-load and re-run the command
   (bounded retries); never re-insert the same events. Only 40001/40P01/55P03 are `ErrRetryable`;
   connection loss and cancellation (`IsUnavailable`) are never retried after a commit attempt.
4. **Contiguous versions.** Versions run 1..n per aggregate. `LoadFromHistory` rejects gaps;
   the projection fails the save (or the rebuild) on a gap rather than skipping.
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
9. **Projection version check.** Apply an event only if `version == stored + 1`; fail on
   `version > stored + 1`. `Projection.Apply` skips `version <= stored`, but a command save treats
   it as an error (the read model is ahead of the event store). State, history and version commit
   in the command's (or the rebuild's) transaction.
10. **Explicit consistency.** Write side: strongly consistent per aggregate. Read side: updated in
    the same transaction, so a committed command is visible to the next query. Aging is computed
    at query time from `stocked_on` and the dealership business date, never stored.
11. **Rebuild.** `rebuild-projections` holds the projection lock exclusively, truncates and replays
    `event_store` in one transaction; commands hold it shared from before the CAS until commit, so
    the replay is a consistent snapshot. Never rebuild from anything but `event_store`.
