# 04 — Event Store

Sections 2, 3 and 7 describe **current code** (`internal/infrastructure/eventstore`). Sections 4–6 are **proposed** changes, all additive.

## 1. MySQL server configuration *(to add, Compose)*

`binlog_format=ROW`, `binlog_row_image=FULL`, `gtid_mode=ON`, `enforce_gtid_consistency=ON`, unique `server_id`, `binlog_expire_logs_seconds` ≥ 3 days. Default isolation is `REPEATABLE READ`.

## 2. Current schema (`repos/schema.sql`, applied by `repos.Migrate`)

```sql
CREATE TABLE IF NOT EXISTS es_aggregate (
    id VARCHAR(255) NOT NULL PRIMARY KEY,         -- aggregate id (vehicle_id)
    version BIGINT NOT NULL,                      -- last committed version (CAS target)
    aggregate_type VARCHAR(255) NOT NULL          -- reflect struct name, e.g. VehicleInventoryAggregate
);

CREATE TABLE IF NOT EXISTS es_event (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, -- unique event id + global insert order
    aggregate_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(255) NOT NULL,              -- reflect struct name, e.g. VehicleStockedEvent
    version BIGINT NOT NULL,
    data TEXT NOT NULL,                            -- JSON payload
    metadata TEXT NULL,                            -- JSON (map), may be "null"
    created_at BIGINT NOT NULL,                    -- unix seconds
    UNIQUE KEY uq_es_event_aggregate_version (aggregate_id, version)
);

CREATE TABLE IF NOT EXISTS es_aggregate_snapshot (
    aggregate_id VARCHAR(255) NOT NULL,
    version BIGINT NOT NULL,
    data TEXT NOT NULL,                            -- JSON of the aggregate
    PRIMARY KEY (aggregate_id, version)
);
```

SQLite tests use an equivalent inline DDL (`repos/schema.go`). Note that `INSERT IGNORE` (MySQL) and `ON CONFLICT DO NOTHING` (SQLite) are dialect-switched in `aggregateRepo.insertAggregate`.

Mapping to the brief's event-store requirements:

| Requirement | Current | Gap |
|---|---|---|
| Unique event ID | `es_event.id` (BIGINT, PK) | none |
| Aggregate ID and type | `es_event.aggregate_id`; type only in `es_aggregate` | type is not on the event row, which matters for CDC (§4) |
| Aggregate version | `es_event.version` | none |
| Event type | `es_event.event_type` | none |
| Event schema version | — | **missing** (§4) |
| Payload and metadata | `data`, `metadata` (TEXT JSON) | none |
| Timestamp | `created_at` (unix s) | precision only (R14) |
| DB-enforced OCC | CAS `UPDATE es_aggregate … WHERE version = ?` + unique `(aggregate_id, version)` | MySQL not yet tested (H1) |
| Atomic multi-event append | `AggregateStore.Save` runs all inserts in one `WithTransaction` | none (tested on SQLite: rollback test) |

## 3. Current write path (`AggregateStore.Save`)

```
BEGIN (gorm Transaction)
  CreateIfNotExist: INSERT IGNORE es_aggregate(id, 0, type); SELECT type → mismatch ⇒ ErrAggregateTypeMismatch
  CheckAndUpdateVersion: UPDATE es_aggregate SET version=:new WHERE id=:id AND version=:base
                         0 rows ⇒ ErrConcurrencyConflict
  for each pending event: INSERT es_event(...)      duplicate (aggregate_id,version) ⇒ ErrDuplicateEvent
  if interval crossed: INSERT es_aggregate_snapshot
COMMIT → root.Update() clears pending events (only on success; tested)
```

Load (`AggregateStore.Get`): `FindAggregate` (version + type check) → optional `ReadSnapshot` → `Get` events `(from, version]` → `LoadFromHistory` (contiguity check) → assert the replayed version equals the stored version.

## 4. Proposed additive changes (Phase 1)

### 4.1 Columns

```sql
ALTER TABLE es_event
  ADD COLUMN aggregate_type VARCHAR(255) NOT NULL DEFAULT 'VehicleInventoryAggregate',
  ADD COLUMN schema_version SMALLINT NOT NULL DEFAULT 1;
```

- `aggregate_type` on the event row lets the projector decode a CDC message on its own through the existing serializer key `<aggregate_type>_<event_type>`, without a join to `es_aggregate`. Debezium only sees the row. `Append` writes the value from the aggregate. The default covers any pre-existing rows (only one aggregate type exists).
- `schema_version` supports payload evolution (03 §5). The default of 1 keeps old rows valid, and `Event.SchemaVersion == 0` is treated as 1.
- **Not changed:** `data`/`metadata` stay `TEXT` (Debezium emits them as strings either way). `created_at` stays unix seconds (R14). `es_event.id` stays the event ID, so no UUID column is needed.

### 4.2 Migration compatibility (D13)

Today `repos.Migrate` runs `CREATE TABLE IF NOT EXISTS` on every start, which can create tables but cannot evolve them. MySQL 8 has no `ADD COLUMN IF NOT EXISTS`. The minimal refactor:

1. Keep `repos.Migrate(db)` as the single entry point and its dialect switch.
2. Replace the single embedded script with numbered embedded scripts per dialect (`migrations/mysql/0001_*.sql`, …) and a `schema_migrations(version, applied_at)` table.
3. `0001` = today's DDL verbatim (with `IF NOT EXISTS`). A database created by the current code is therefore adopted without error, and `0001` is recorded as applied.
4. `0002` = the `ALTER TABLE` above (SQLite: two `ALTER TABLE … ADD COLUMN … DEFAULT` statements, which SQLite supports).
5. MySQL DDL is not transactional. Each script is applied, then recorded. A failure leaves a clear "version N not recorded" state to fix manually. This is acceptable for a take-home and documented.

The existing SQLite tests keep calling `repos.Migrate` unchanged.

### 4.3 Append-only enforcement *(to add)*

- **Grants:** the application user gets `SELECT, INSERT` on `es_event`, `SELECT, INSERT, UPDATE` on `es_aggregate` (CAS), and `SELECT, INSERT` on `es_aggregate_snapshot`. It has no `UPDATE`/`DELETE` on `es_event`. The Debezium user gets `SELECT, RELOAD, SHOW DATABASES, REPLICATION SLAVE, REPLICATION CLIENT`.
- **MySQL triggers (optional, open decision O1):** `BEFORE UPDATE` / `BEFORE DELETE ON es_event` → `SIGNAL SQLSTATE '45000'`. Each trigger is a single statement, so it is compatible with the existing `splitSQL` (split on `;`). Triggers would also block the existing tests that `UPDATE`/`DELETE es_event` to simulate corruption once those tests run on MySQL. Phase 1 therefore enforces append-only by **grants only**.

### 4.4 Debezium compatibility

- Capture **only** `es_event` (`table.include.list`). Writes to `es_aggregate` and snapshots stay on the write side.
- `es_event` has a primary key (`id`), which Debezium requires. Override the message key to `aggregate_id` (R1).
- Column mapping: `id`/`version`/`created_at`/`schema_version` → numbers; `aggregate_id`/`aggregate_type`/`event_type` → strings; `data`/`metadata` → strings that contain JSON. There are no temporal or binary types, so there are no conversion pitfalls.
- Rows are insert-only (grants + triggers), so the projector accepts only `op ∈ {c, r}`.

## 5. Concurrency, retries, duplicates

The OCC mechanism stays as implemented (D15): **CAS on `es_aggregate.version` inside the save transaction**, with the unique `(aggregate_id, version)` as a second, independent guard.

On MySQL (InnoDB, REPEATABLE READ), two concurrent saves from base version 3 behave like this. Both execute `UPDATE … WHERE version = 3`. The second blocks on the row lock until the first commits, then re-evaluates against the latest committed row, matches 0 rows, and fails with `ErrConcurrencyConflict`. **This is expected behaviour; it has not yet been verified on MySQL.** The existing `TestOptimisticConcurrency` runs sequentially on SQLite.

**H1 (to verify in Phase 1):** `CreateIfNotExist` runs `INSERT IGNORE` on every save. On a duplicate key, InnoDB takes a **shared** lock on the existing row. Two concurrent transactions holding S locks that both then `UPDATE` (needing X) can deadlock, so one gets **1213** instead of a clean 0-row CAS. Today 1213 is not mapped and would surface as a generic error. Candidate fixes, applied only if the test confirms H1:
- skip `CreateIfNotExist` when `BaseVersion() > 0` (the aggregate is known to exist)
- map 1213 and 1205 → `ErrConcurrencyConflict` (retryable)

| Scenario | Behaviour (application layer, Phase 2) |
|---|---|
| Two managers propose concurrently, no `If-Match` | The loser gets `ErrConcurrencyConflict`, then the command handler re-loads, re-runs the command method (max 3 attempts, jittered) and now sees the active action → `409 ACTION_ALREADY_ACTIVE` |
| Two revisions, no `If-Match` | Both apply in order; both are in history |
| Two revisions with `If-Match` | The handler compares the loaded version with `If-Match` before the command → `412` |
| Client retry after a timeout, no key | Caught by domain rules (propose → 409, sell → 409, identical revise → no-op 200) |
| Client retry with `Idempotency-Key` | **Optional** (§6) |
| MySQL down | `503`; the tx guarantees no partial state |

Retry always re-loads and re-validates. A failed insert is never retried blindly.

## 6. Optional extensions (time permitting, see 07 cut list)

- **VIN reservation (D4):** a `vin_reservation(vin PK, vehicle_id UNIQUE)` table written inside the same `WithTransaction` as the stock/sell events. This would be a narrow hook in `Save`, or an application-level `WithTransaction` callback.
- **Idempotency-Key store:** an `idempotency_key(actor_id, key PK, fingerprint, response)` table, inserted in the same tx. Without it, domain rules still give natural idempotency.

## 7. Snapshots (existing; disabled for vehicles)

Current behaviour: when a save crosses a multiple of the interval (default 10 in `NewAggregateStore`), `CreateSnapshot` stores `serializer.Marshal(agg)`. `Get` restores the newest snapshot ≤ the aggregate version with `Unmarshal`, then replays only the later events. The existing tests pass because their test aggregate implements `MarshalJSON`/`UnmarshalJSON`.

**Verified hazard (probe run 2026-10-29 on a scratch copy, not committed):** an aggregate that keeps state in unexported fields and has no custom JSON is snapshotted as `{}`. On load the snapshot "restores" nothing and the earlier events are skipped. The reloaded aggregate had the right version (3) but empty state (`vin=""`, counter 0 instead of 3). `ReadSnapshot` only guards against an empty string, not `{}`. `VehicleInventoryAggregate` (03 §2) has exactly this shape.

Decision (D2): construct the vehicle store with `NewAggregateStoreWithSnapshotInterval(repos, 0)`, which is already supported and disables snapshots. Vehicle streams are short (typically < 10 events), so snapshots bring no measurable benefit. Re-enabling requires (a) `MarshalJSON`/`UnmarshalJSON` on the aggregate and (b) a test that compares the snapshot-restored state with the replayed state.
