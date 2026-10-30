# 04 — Event Store

> **Superseded in Phase 7.** Sections 1–5 and 8 describe the MySQL 8.4 event store of Phases 2–6 and are kept as history (the decisions and findings, such as H1, explain the current design). The current event store is the PostgreSQL `event_store` schema: **see [§9](#9-phase-7-postgresql-event_store-current)**. Sections 6 (open extensions) and 7 (snapshots) still apply.

**Status (historical): Phase 2 implemented and verified on MySQL 8.4.13 (2026-10-29).** Section 6 lists optional extensions that are still open. Verification commands and results: section 8.

## 1. MySQL server configuration (implemented: `docker-compose.yaml`)

Service `mysql` (`docker.io/library/mysql:8.4`), bound to `127.0.0.1:${MYSQL_PORT}`. Credentials come only from `.env`, which is git-ignored (template: `.env.example`). Binlog settings for later Debezium: `--server-id=1 --gtid-mode=ON --enforce-gtid-consistency=ON --binlog-row-image=FULL --binlog-expire-logs-seconds=259200` (ROW format and the binary log are the 8.4 defaults). Verified on the running container: `8.4.13 · gtid_mode=ON · binlog_format=ROW · binlog_row_image=FULL · log_bin=1 · REPEATABLE-READ`.

Local workflow: `cp .env.example .env` (set the passwords) → `make mysql-up` → `make test-integration`. Migrations run with an admin account. `make mysql-grants` then creates the restricted application user (§4.3).

## 2. Original schema = migration version 1 (`repos/schema.sql`, unchanged)

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
  CreateIfNotExist: INSERT es_aggregate(id, 0, type) ON DUPLICATE KEY UPDATE id = id   (was INSERT IGNORE, see §5)
                    SELECT type → mismatch ⇒ ErrAggregateTypeMismatch
  CheckAndUpdateVersion: UPDATE es_aggregate SET version=:new WHERE id=:id AND version=:base
                         0 rows ⇒ ErrConcurrencyConflict
  for each pending event: INSERT es_event(..., aggregate_type, schema_version)   duplicate ⇒ ErrDuplicateEvent
  if interval crossed: INSERT es_aggregate_snapshot
COMMIT → root.Update() clears pending events (only on success; tested)
any error → rollback; MySQL 1213/1205 wrapped as ErrRetryable (repos.WithTransaction)
```

Load (`AggregateStore.Get`): `FindAggregate` (version + type check) → optional `ReadSnapshot` → `Get` events `(from, version]` → `LoadFromHistory` (contiguity check) → assert the replayed version equals the stored version.

## 4. Schema evolution (implemented)

### 4.1 Version 2: `es_event.aggregate_type`, `es_event.schema_version`

| Column | MySQL type | Existing rows | New rows |
|---|---|---|---|
| `aggregate_type` | `VARCHAR(255) NOT NULL DEFAULT ''` | backfilled from `es_aggregate.aggregate_type` | set by `AggregateStore.Save` from the aggregate (`VehicleInventoryAggregate`) |
| `schema_version` | `INT NOT NULL DEFAULT 1` | 1 | `Event.SchemaVersion`, where 0 means `eventsourcing.DefaultSchemaVersion` (1) |

- `eventsourcing.Event` gained the matching `AggregateType` and `SchemaVersion` fields (additive, `omitempty`). `eventRepo.Get` reads both back. A non-empty `aggregate_type` that differs from the aggregate being loaded fails with `ErrAggregateTypeMismatch`. An empty value is tolerated for rows appended directly through the repository without a type.
- **Not changed:** stored event-type names (Go struct names), `data`/`metadata` as `TEXT` JSON, `created_at` (unix seconds), `es_event.id`. **No event UUID (decision Q2):** `(aggregate_id, version)` is the stable per-stream identity that consumers use for idempotency. `es_event.id` stays the database-generated surrogate.
- In MySQL 8, `ADD COLUMN … DEFAULT` on InnoDB is an instant metadata change, so there is no table rebuild.
- **Read guard:** `eventRepo.Get` refuses any `schema_version` other than 1 with `ErrUnsupportedSchemaVersion`. Decoding a newer payload with today's struct could silently drop fields, for example during a rollback. When an event type first changes shape, add an upcaster at that point instead of the refusal.
- **Write guard:** `eventRepo.Append` requires `aggregate_type`. The backfill ran once, so an untyped row written later would stay undecodable for CDC. `AggregateStore.Save` always sets it.
- **Backfill is a one-off admin edit of history:** an exception to append-only, done by the migration, never by the application. If Debezium were attached before it ran, it would emit `u` (update) events, which the projector must ignore: it accepts only `op ∈ {c, r}`.

### 4.2 Versioned migrator (inside the existing `repos.Migrate`)

- Each applied version is recorded in `es_schema_migrations(version, name, applied_at)`.
- **Version 1** is the original `CREATE TABLE IF NOT EXISTS` DDL, unchanged. A database created by the old unversioned `Migrate` is therefore adopted without error.
- **Version 2** is the columns above.
- **Version 3** (Phase 3) is the `dealership(dealership_id, name, timezone)` reference table, seeded idempotently with `d-001` (Europe/London) and `d-002` (Australia/Sydney). The application user only has `SELECT` on it.
- **Partial runs:** MySQL DDL is not transactional, so each step is idempotent. A column is added only if `information_schema.COLUMNS` (or SQLite's `pragma_table_info`) shows it missing, and the backfill only touches rows `WHERE aggregate_type = ''`. A run that stopped after the first `ALTER` completes on the next run (tested).
- **Concurrent migrators:** on MySQL they are serialised with `GET_LOCK(CONCAT('es_migrate:', MD5(DATABASE())))` on one pinned connection (`db.Connection`). The name is hashed because lock names are limited to 64 characters. Tested with 4 parallel callers.
- **Newer schema:** if `es_schema_migrations` holds a version above the newest this build knows, `Migrate` fails, so an older release doesn't run against a schema it doesn't understand (tested).
- The signature `repos.Migrate(db *gorm.DB) error` is unchanged. All existing callers and tests keep working.

### 4.3 Append-only enforcement (implemented: `deploy/mysql/grants.sql`)

The restricted application user (`{{app_user}}`, applied after migrations) gets:
- `SELECT, INSERT, UPDATE` on `es_aggregate`: version CAS, plus `ON DUPLICATE KEY UPDATE`, which needs UPDATE;
- `SELECT, INSERT` on `es_event` and `es_aggregate_snapshot`;
- `SELECT` on `es_schema_migrations`.

There is **no UPDATE/DELETE on `es_event`**. Verified by `TestMySQLAppUserCannotRewriteHistory`: the user can save and load, including writing snapshots. `UPDATE`/`DELETE`/`TRUNCATE es_event`, `DELETE es_aggregate`, `ALTER` and `DROP` all fail with MySQL error 1142. Migrations and the tests that deliberately corrupt rows use the privileged test connection. Triggers were not added (O1).

### 4.4 CDC readiness (Debezium not implemented yet)

- Each `es_event` row is self-describing: `aggregate_id`, `aggregate_type`, `event_type`, `version`, `schema_version`, `data`, `metadata`, `created_at`. A consumer can decode it with the serializer key `<aggregate_type>_<event_type>` without joining `es_aggregate`.
- Rows are insert-only (grants), so Debezium only needs `op ∈ {c, r}`. Capture `es_event` only, and set `message.key.columns=<db>.es_event:aggregate_id` so one aggregate maps to one partition.
- **Ordering contract:** consumers order and deduplicate by `(aggregate_id, version)`. `es_event.id` (AUTO_INCREMENT) is allocated at insert time, **not** commit time, so global `id` order must not be treated as commit order. Within one aggregate, the CAS serialises writers, so `id` order matches `version` order there.
- Projection idempotency: apply only when `version == last_applied + 1`, as in 05 §4.

## 5. Concurrency, retries, duplicates (verified on MySQL)

OCC is unchanged: **CAS on `es_aggregate.version` inside the save transaction**, with the unique `(aggregate_id, version)` as a second guard.

**H1 confirmed and fixed.** Before any change, `TestMySQLConcurrentWritersSameExpectedVersion` (16 writers from the same version) gave `ok=1 conflicts=9 other=6`. The 6 others were `Error 1213: Deadlock found`. The creation test gave 1 deadlock out of 16. InnoDB's `LATEST DETECTED DEADLOCK` showed both transactions **holding `lock mode S locks rec but not gap`** on the `es_aggregate` PRIMARY row and **waiting for `lock_mode X`**. `INSERT IGNORE` takes a shared lock on the duplicate row, and the CAS `UPDATE` then needs an exclusive one: a classic S→X upgrade deadlock.
- **Fix (one statement, MySQL only):** `INSERT … ON DUPLICATE KEY UPDATE id = id`. It takes an **exclusive** lock on the existing row, so the second writer waits at the insert and then fails the CAS cleanly. It also stops `IGNORE` from downgrading other errors (such as truncation) into warnings. SQLite keeps `ON CONFLICT DO NOTHING`.
- **New locking order:** writers to the same aggregate now queue at `CreateIfNotExist`, at the start of Save, instead of meeting at the CAS. Under REPEATABLE READ, the following type `SELECT` is the transaction's first consistent read, taken after the lock wait, so it sees the committed row.
- **1213 stays mapped as retryable.** It was really observed in this session (pre-fix run). Other deadlock paths can still occur, for example once a transaction touches several aggregates, and InnoDB rolls the transaction back in every such case.
- **After the fix** (run 3× in a row): 10 rounds × 16 writers give exactly 1 success and 15 `ErrConcurrencyConflict`, with 0 other errors and versions `[1, 2]`. The same holds for concurrent creation of a new aggregate. 16 independent aggregates all succeed.
- **No lost updates:** 16 writers that reload and re-run on `IsRetryable` end at version 17 with every event present.
- **Mutation check:** restoring `INSERT IGNORE` makes both concurrency tests fail again.

**Error classification** (`repos/errors.go`, typed `*mysql.MySQLError`):

| Situation | Returned | `IsRetryable` |
|---|---|---|
| CAS matched 0 rows | `ErrConcurrencyConflict` | yes: reload and re-run the command |
| 1062 on `es_event (aggregate_id, version)` | `ErrDuplicateEvent` | no (indicates inconsistent store state) |
| 1213 deadlock, 1205 lock wait timeout | `ErrRetryable` wrapping the MySQL error | yes: reload and re-run the command |
| context cancelled / deadline | `context.Canceled` / `DeadlineExceeded` | no |
| connection closed, other SQL errors | unchanged | no |
| domain rule violation | domain errors, raised before Save | no |

- 1205 is **verified with a real lock wait**. An admin transaction holds `SELECT … FOR UPDATE` and the store uses `innodb_lock_wait_timeout=1`. The failed save keeps its pending events, and the **same** save succeeds after the lock is released, since nothing was persisted.
- **The store never retries.** Re-saving stale pending events would be wrong. `internal/application/command.Service.execute` (Phase 3) reloads the aggregate, re-runs the command against fresh state and saves, with at most 3 attempts. It never retries with `If-Match`, on domain errors, or on `IsUnavailable` errors.
- **Metadata (Phase 3):** `AggregateStore.SaveWithMetadata(ctx, agg, md)` merges command metadata (`actor_id`, `correlation_id`, `traceparent`) into the cloned events at save time. Keys an event already carries win, the aggregate's pending events are never modified, and `Save` is `SaveWithMetadata(…, nil)`.
- **Duplicate detection** checks the typed MySQL error number first, then the gorm sentinel, then a text match. The text match is kept for SQLite, which would otherwise require importing the cgo driver into production code.

| Scenario | Behaviour |
|---|---|
| Two managers propose concurrently | Loser gets `ErrConcurrencyConflict`. `command.Service` (Phase 3) reloads, re-runs, sees the active action, and returns `409 ACTION_ALREADY_ACTIVE`. Verified over HTTP on MySQL: 5 rounds × 16 requests give 1 × 201 and 15 × 409 each round. |
| Two revisions with `If-Match` | Handler compares versions before the command and returns `412` |
| Retry without an idempotency key | Caught by domain rules (04 §6 for the optional key store) |
| MySQL down / connection lost | Non-retryable infrastructure error. The transaction guarantees no partial state. |

## 6. Optional extensions (time permitting, see 07 cut list)

- **VIN reservation (D4):** a `vin_reservation(vin PK, vehicle_id UNIQUE)` table written inside the same `WithTransaction` as the stock/sell events. This would be a narrow hook in `Save`, or an application-level `WithTransaction` callback.
- **Idempotency-Key store:** an `idempotency_key(actor_id, key PK, fingerprint, response)` table, inserted in the same tx. Without it, domain rules still give natural idempotency.

## 7. Snapshots (capability kept; disabled for vehicles at runtime)

- **Capability:** when a save crosses a multiple of the interval, the store writes `serializer.Marshal(agg)`. `Get` restores the newest snapshot and replays only the later events. That is unchanged (in Phase 2 the snapshot tests also ran on MySQL; since Phase 7 on SQLite and PostgreSQL).
- **Vehicle snapshots work:** `VehicleInventoryAggregate` implements `MarshalJSON`/`UnmarshalJSON` (Phase 1). `TestVehicleSnapshotRestoreThenAppend` (SQLite and MySQL, interval 2) deletes the events covered by the snapshot, restores the full state from the snapshot, sells the vehicle, appends and reloads. Snapshots are written at versions 2 and 4.
- **Runtime decision (Q6): interval 0 for vehicles.** Use `NewAggregateStoreWithSnapshotInterval(repos, 0)` when wiring the application in Phase 3.
  - Why: a vehicle stream is short (stock, a few action changes, sale; typically < 10 events), so a full replay is a single indexed range read.
  - Snapshots would add a write every N events plus stored JSON that must stay schema-compatible, for no measurable read saving.
  - `TestVehicleLifecycleReplayFromStore` runs with interval 0 and asserts that no snapshot row is written.
- **Snapshot JSON has no format version** (existing schema). If the aggregate's state shape changes, old snapshots must be cleared or ignored, or they'll decode silently. Low risk while vehicles run at interval 0.
- **History:** the Phase 0 probe found that an aggregate with unexported state and no custom JSON is snapshotted as `{}`. `ReadSnapshot` still only rejects an empty string, not `{}`. That gap is harmless for vehicles, which have custom JSON and run at interval 0, and is documented as a risk for any future aggregate.

## 8. Phase 2 verification (2026-10-29, MySQL 8.4.13 via podman-compose)

| Command | Result |
|---|---|
| `make check` (gofmt, `go vet` with and without `-tags integration`, `go test -count=1 ./...`, `go test -race -count=1 ./...`) | all pass |
| `make test-integration` (`MYSQL_TEST_DSN` set) | 28 tests in `eventstore` + 2 in `repos`, all pass (after the review fixes) |
| `go test -tags integration -race -count=1 ./internal/infrastructure/eventstore/...` | pass |

- Each integration test gets its own `es_it_<random>` database, dropped at cleanup. After the runs: 0 leftover databases and 0 leftover `es_app_*` users.
- **Mutation checks** (scratch copies, MySQL), each caught by the tests targeting it:
  - `INSERT IGNORE` restored → both concurrency tests fail;
  - classifier never marks 1213/1205 → `TestClassifyTxError`, lock-wait test fail;
  - backfill matches nothing → upgrade and resume tests fail;
  - column-exists check removed → resume test fails;
  - `aggregate_type` not stamped → fresh-migration and vehicle tests fail.
- `make mysql-grants` (the `sed` substitution into the running container) was **not** executed end to end. The same `grants.sql` is applied by the integration test.

## 9. Phase 7: PostgreSQL `event_store` (current)

Since Phase 7 the event store is the schema `event_store` of the single PostgreSQL 17 database (default name `inventory_read`). Event names, payloads, the serializer, `AggregateStore` and its CAS are unchanged. Code: `internal/infrastructure/eventstore/repos/{migrate.go,migrations.go,event.go,event_stream.go,aggregate.go,errors.go}`, `aggregate_store.go`, `transactional_projection.go`.

### 9.1 Schema (`repos/migrations.go`)

| Version | Content |
|---|---|
| 1 | `es_aggregate(id TEXT PK, version BIGINT, aggregate_type TEXT)`; `es_event(id BIGINT GENERATED BY DEFAULT AS IDENTITY PK, aggregate_id → es_aggregate, aggregate_type, event_type, version, schema_version DEFAULT 1, data TEXT, metadata TEXT, created_at BIGINT, UNIQUE(aggregate_id, version))` with non-empty and range `CHECK`s; `es_aggregate_snapshot(aggregate_id, version, data, PK(aggregate_id, version))` |
| 2 | `dealership(dealership_id PK, name, timezone)`, seeded `d-001` (Europe/London) and `d-002` (Australia/Sydney) with `ON CONFLICT DO NOTHING`. The canonical reference data: the command side reads the time zone here, and the read model's foreign key and queries point here too. |

- The PostgreSQL schema starts at version 1: the MySQL v1–v3 steps (additive columns, backfill) are not replayed. Their end state is the v1 DDL above plus v2.
- `id` is an identity column (`BY DEFAULT`, kept from Phase 7, when a one-off MySQL import used explicit ids; that import was removed in Phase 7.1). The application never supplies an id; it reads the generated one back with `RETURNING`.
- Every table name in SQL is schema-qualified. The unit tests run the same SQL on SQLite with an attached database named `event_store` (`internal/testsupport/sqlitetest`); `migrations.go` keeps a SQLite variant with the same tables, columns and unique constraints.

### 9.2 Migrator (`repos/migrate.go`)

- Bootstrap (`CREATE SCHEMA IF NOT EXISTS event_store` and `event_store.schema_migrations`) and every version run under `pg_advisory_xact_lock(51001, 0)`, because `CREATE … IF NOT EXISTS` is not safe against a concurrent creator in PostgreSQL.
- Each version is applied and recorded in one transaction (PostgreSQL DDL is transactional): a failed version leaves nothing behind and is retried by the next run.
- A database with a newer version than the build knows is refused. Concurrent callers are tested.
- `migrate` runs as the admin role; the read model's migrations follow (05 §8).

### 9.3 Write path (`AggregateStore.SaveWithMetadata`)

```
refuse if already inside a transaction (ErrNestedSave)
BEGIN
  projection lock: pg_advisory_xact_lock_shared(52003, 0)          -- before any event-store row lock
  CreateIfNotExist: INSERT es_aggregate(id, 0, type) ON CONFLICT (id) DO NOTHING
                    SELECT type → mismatch ⇒ ErrAggregateTypeMismatch
  CheckAndUpdateVersion: UPDATE es_aggregate SET version = :new WHERE id = :id AND version = :base
                         0 rows ⇒ ErrConcurrencyConflict
  for each event: INSERT es_event(…) RETURNING id                    -- id written back into the event
                  unique violation ⇒ ErrDuplicateEvent
  Projection.Apply(events) on the same *gorm.DB transaction         -- 05 §8; no savepoints
  if interval crossed: INSERT es_aggregate_snapshot
COMMIT → root.Update() acknowledges the pending events (only after commit)
any error → rollback of all of it; pending events kept
```

- `ON CONFLICT DO NOTHING` makes a concurrent creator wait for the first creator's commit instead of failing; the CAS then decides the winner. (On MySQL the equivalent needed `ON DUPLICATE KEY UPDATE` to avoid the H1 deadlock, §5.)
- Without a projection (`WithProjection` not set, as in unit tests) the save is the same minus the lock and the projection.

### 9.4 Error classification (`repos/errors.go`, SQLSTATE via `pgconn.PgError`)

| Situation | Returned | `IsRetryable` | HTTP (commands) |
|---|---|---|---|
| CAS matched 0 rows | `ErrConcurrencyConflict` | yes: reload and re-run | 409 `CONCURRENCY_CONFLICT` after 3 attempts, or the domain rule's 409 |
| `23505` on `es_event (aggregate_id, version)` | `ErrDuplicateEvent` | no (inconsistent store state; the CAS normally prevents it) | 500 |
| `40001` serialization failure, `40P01` deadlock, `55P03` lock not available | `ErrRetryable` | yes | 503 `UNAVAILABLE` after 3 attempts |
| `57014` query cancelled, `57P01`–`57P03` shutdown, class `08` connection errors, network errors, bad connection | unchanged, `IsUnavailable` = true | **no**: around a commit the outcome is unknown | 503 `UNAVAILABLE` |
| context cancelled / deadline | `context.Canceled` / `DeadlineExceeded` | no | 503 `UNAVAILABLE` |
| projection error (gap, read model ahead, FK, unknown event) | wrapped projection error | no | 500 |

Covered by PostgreSQL integration tests: a save blocked past `lock_timeout` is retryable and the same save succeeds once the lock is released; context errors are not retryable.

### 9.5 Concurrency (PostgreSQL integration tests)

`postgres_concurrency_integration_test.go`: N writers from the same version → exactly one success, the rest `ErrConcurrencyConflict`, contiguous versions; concurrent creation of the same aggregate likewise; independent aggregates all succeed; writers that reload and re-run on `IsRetryable` lose no update.

### 9.6 Append-only enforcement (`deploy/postgres/app-grants.sql`)

The application writer `inventory_app` has `SELECT, INSERT, UPDATE` on `es_aggregate`; `SELECT, INSERT` on `es_event` plus `USAGE` on its identity sequence; `SELECT, INSERT` on `es_aggregate_snapshot`; `SELECT` on `dealership`; no `UPDATE`, `DELETE` or `TRUNCATE` on `es_event`, no DDL, `NOSUPERUSER`. The rebuild role can only `SELECT` from `es_event`; the read-only query role cannot read it. Tested by `internal/presentation/postgres_roles_integration_test.go` and `viewstore/privileges_pg_integration_test.go`.

### 9.7 Reading the history

- `ReadAll` streams every event ordered by `(aggregate_id, version)` in keyset batches on the unique index, on the caller's transaction (the rebuild's). Ordering never relies on `es_event.id`.
- `ReadStream` reads one aggregate's version range.
