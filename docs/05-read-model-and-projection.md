# 05 — Read Model, CDC and Projection

Current code: `internal/infrastructure/projection` (a `Projection` pipeline, the `Projector` interface, and stubs `VehicleProjector`/`OutboxProjector`) and `internal/infrastructure/viewstore` (a `ViewStore` interface with `Save(ctx, ev)`). **There is no Postgres code, Kafka code or read-model schema yet.** Everything below is proposed unless marked *existing*.

## 1. PostgreSQL schema *(to add)*

```sql
CREATE TABLE dealership (                         -- reference data (same seed as MySQL)
  dealership_id TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  timezone      TEXT NOT NULL                     -- IANA
);

CREATE TABLE inventory_vehicle (
  vehicle_id         TEXT PRIMARY KEY,            -- = es_event.aggregate_id
  dealership_id      TEXT     NOT NULL REFERENCES dealership,
  vin                CHAR(17) NOT NULL,
  make               TEXT     NOT NULL,
  model              TEXT     NOT NULL,
  model_year         SMALLINT NOT NULL,
  stocked_on         DATE     NOT NULL,
  status             TEXT     NOT NULL CHECK (status IN ('IN_STOCK','SOLD')),
  sold_on            DATE,
  action_id          TEXT,                        -- current action (NULL = none)
  action_type        TEXT,
  action_note        TEXT,
  action_proposed_at TIMESTAMPTZ,
  action_updated_at  TIMESTAMPTZ,
  action_updated_by  TEXT,
  version            BIGINT      NOT NULL,        -- last applied es_event.version (projection progress)
  last_event_id      BIGINT      NOT NULL,        -- es_event.id
  updated_at         TIMESTAMPTZ NOT NULL
);

CREATE TABLE vehicle_action_history (
  event_id          BIGINT PRIMARY KEY,           -- es_event.id
  vehicle_id        TEXT   NOT NULL REFERENCES inventory_vehicle ON DELETE CASCADE,
  version           BIGINT NOT NULL,
  action_id         TEXT   NOT NULL,
  change            TEXT   NOT NULL CHECK (change IN ('PROPOSED','REVISED','CANCELLED','CLOSED_BY_SALE')),
  action_type       TEXT   NOT NULL,
  note              TEXT,
  reason            TEXT,
  actor_id          TEXT,
  occurred_at       TIMESTAMPTZ NOT NULL,         -- from es_event.created_at
  UNIQUE (vehicle_id, version)
);

-- optional (D5): only if Kafka offsets are also stored in Postgres
CREATE TABLE projection_checkpoint (
  projection TEXT, topic TEXT, partition INT, next_offset BIGINT NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (projection, topic, partition)
);
```

There is deliberately **no `days_in_stock` or `is_aging` column**.

### Indexes

```sql
CREATE INDEX ix_vehicle_dealer_status_stocked
  ON inventory_vehicle (dealership_id, status, stocked_on, vehicle_id);
CREATE INDEX ix_vehicle_dealer_make_model
  ON inventory_vehicle (dealership_id, status, lower(make), lower(model), stocked_on, vehicle_id);
-- history: covered by UNIQUE (vehicle_id, version)
```

Reads are always single-dealership. Per-dealership volume is small (01 A12), so the indexes target the default list, aging and make/model filters, and no more. An `EXPLAIN` test on a seeded dataset checks that they are used.

## 2. Query-time aging

The application resolves `business_date` (today in the dealership time zone, or `as_of`), then:

```sql
SELECT v.*, ($bd::date - v.stocked_on)                            AS age_days,
       (v.status = 'IN_STOCK' AND v.stocked_on < $bd::date - 90) AS is_aging
FROM inventory_vehicle v
WHERE v.dealership_id = $dealer
  AND v.status = ANY($statuses)                                   -- default {IN_STOCK}
  [AND lower(v.make)  = lower($make)]
  [AND lower(v.model) = lower($model)]
  [AND v.stocked_on <= $bd::date - $min_age]                      -- age >= min
  [AND v.stocked_on >= $bd::date - $max_age]                      -- age <= max
  [AND v.status = 'IN_STOCK' AND v.stocked_on < $bd::date - 90]   -- aging=true
  [AND keyset predicate]
ORDER BY v.stocked_on ASC, v.vehicle_id ASC
LIMIT $limit + 1;
```

Bracketed predicates are emitted only when the filter is present. `age_days > 90 ⇔ stocked_on < bd − 90`, so exactly 90 is not aging. Sold vehicles report `age_days = sold_on − stocked_on`.

| `sort` | ORDER BY | Keyset |
|---|---|---|
| `age_desc` (default, oldest first) | `stocked_on, vehicle_id` | `(stocked_on, vehicle_id) > (…)` |
| `age_asc` | `stocked_on DESC, vehicle_id DESC` | `(…) < (…)` |
| `make_model` *(optional)* | `lower(make), lower(model), stocked_on, vehicle_id` | 4-column row comparison |

The cursor is base64url JSON `{sort, last, as_of, filter_hash}`. It pins `as_of`, so paging across midnight stays consistent, and a filter mismatch returns 400. Limit default 20, max 100.

## 3. CDC: Debezium → Kafka *(to add)*

```json
{
  "connector.class": "io.debezium.connector.mysql.MySqlConnector",
  "topic.prefix": "inventory",
  "database.include.list": "inventory",
  "table.include.list": "inventory.es_event",
  "message.key.columns": "inventory.es_event:aggregate_id",
  "snapshot.mode": "initial",
  "include.schema.changes": "false",
  "schema.history.internal.kafka.topic": "inventory.schema-history",
  "transforms": "unwrap",
  "transforms.unwrap.type": "io.debezium.transforms.ExtractNewRecordState",
  "transforms.unwrap.add.fields": "op",
  "transforms.unwrap.drop.tombstones": "true",
  "key.converter": "org.apache.kafka.connect.json.JsonConverter",
  "key.converter.schemas.enable": "false",
  "value.converter": "org.apache.kafka.connect.json.JsonConverter",
  "value.converter.schemas.enable": "false"
}
```

- Only `es_event` is captured. The key is `aggregate_id`, so all events of one vehicle land on one partition in commit order (R1). The topic is `inventory.inventory.es_event` with 3 partitions.
- The value is a flat row: `{id, aggregate_id, aggregate_type, event_type, version, schema_version, data, metadata, created_at, __op}`. The consumer (`internal/application/messaging`) converts it into an *existing* `eventsourcing.Event`. It decodes `data` with the *existing* serializer: `Type(aggregate_type, event_type)` gives the factory, then `Unmarshal`. This is the concrete reason for the `es_event.aggregate_type` column (04 §4.1).
- Delivery is **at-least-once** end to end. Nothing assumes exactly-once across MySQL → Kafka → Postgres.

## 4. Projector (built on the existing abstractions)

### 4.1 Narrow refactor of the existing interfaces

```go
// projectors/interface.go — existing:  Handle(ctx, ev eventsourcing.Event) error
// proposed (narrow): the projector receives the transaction-scoped store
type Projector interface {
    Handle(ctx context.Context, store viewstore.ViewStore, ev eventsourcing.Event) error
}

// viewstore/viewstore.go — existing: Save(ctx, ev) error ; extended:
type ViewStore interface {
    WithTransaction(ctx context.Context, fn func(tx ViewStore) error) error // mirrors repos.EventStore.WithTransaction
    AggregateVersion(ctx context.Context, aggregateID string) (int, error)  // locks the row (FOR UPDATE)
    // read-model writes used by VehicleProjector, e.g. UpsertVehicle, SetAction, AppendHistory
    // read-model queries used by application/query
}
```

**Why the signature changes:** state, version and history must commit in **one** Postgres transaction. The existing `Handle(ctx, ev)` has no way to reach a transaction. Passing the tx-bound store explicitly mirrors the existing `repos.EventStore.WithTransaction(ctx, func(txStore EventStore) error)` pattern instead of hiding a tx in `context`. Only two stub implementations are affected.

`Projection.Apply(ctx, ev)` (existing) keeps its signature. Its body becomes:

```
viewStore.WithTransaction(ctx, func(tx) {
    current := tx.AggregateVersion(ev.AggregateID)            // 0 if absent, row locked
    if ev.Version <= current      → duplicate: return nil      (metric)
    if ev.Version >  current + 1  → gap: return ErrProjectionGap (halt; repair optional)
    for each stage: run projectors with tx (stages of 1 projector, as today)
})
```

The `errgroup` branch of `runStage` (stages with more than one projector) is **not used** for the read model, because a single Postgres transaction cannot be used concurrently. It stays in the code for non-transactional projectors. `OutboxProjector` is not registered (Debezium on `es_event` already plays the outbox role) and is left untouched.

### 4.2 Ordering, progress and restart

- Kafka orders events within a partition, and the key keeps each vehicle in one partition. The consumer processes **each partition sequentially**.
- **Progress, mandatory:** `inventory_vehicle.version` is committed in the same transaction as the state change. Re-delivery after a crash is therefore harmless: either the whole tx was lost and the record is re-applied, or it committed and the record is skipped as a duplicate.
- The Kafka offset is committed to Kafka **after** the Postgres commit (at-least-once).
- **Optional (D5):** also store the offset in `projection_checkpoint` within the same tx, and seek to it on partition assignment. Without this, the only cost is re-reading some already-applied records after a restart.

### 4.3 Gaps and out-of-order versions

- `version ≤ current` means a duplicate or stale record. It is skipped.
- `version > current + 1` means missing events. **Mandatory:** detect it, roll back, pause that partition with backoff, log at ERROR with aggregate and version context, set `projection_halted{partition}=1`, and fail readiness. Records are never skipped.
- **Optional (D6):** repair by reading the missing versions from MySQL with the new `repos.ReadStream` (04 / 02 §5) inside the same tx, then continue.
- Unknown `aggregate_type_event_type` in the serializer (existing `Type()` returns `ok=false`) is handled the same way: halt, never skip.

### 4.4 Rebuild and catch-up (does not depend on Kafka retention; D14)

`main rebuild` (and the early E2E feed before Kafka exists):

1. Take a Postgres advisory lock. The live projector holds it for its session, so a rebuild fails fast while the projector is running.
2. (Rebuild only) `TRUNCATE inventory_vehicle, vehicle_action_history`.
3. Stream `es_event` with `repos.ReadAll(afterID, 1000)`, which is `WHERE id > ? ORDER BY id LIMIT ?`. Decode each event with the existing serializer and pass it through the **same** `Projection.Apply`.
4. Release the lock.

Per-aggregate order holds because within one aggregate, `id` order equals `version` order. The CAS serialises writers, so a later version is always inserted after the earlier one committed. The version check guards against anything else. When the projector restarts, the records it re-reads from Kafka are skipped as duplicates by version.

The same reader in "catch-up" mode, without the truncate, gives a working **MySQL → Postgres** path before Debezium is wired. That makes the business flow (stock → aging list → propose action → history) testable end to end early. It is a test and recovery path, not the live transport. The live transport remains Debezium/Kafka, because polling by `id` can skip in-flight transactions (02 §1).

### 4.5 Sequence

```mermaid
sequenceDiagram
  participant API
  participant MySQL
  participant Debezium
  participant Kafka
  participant Consumer as messaging consumer
  participant Proj as Projection.Apply
  participant PG as Postgres
  API->>MySQL: BEGIN; CAS es_aggregate; INSERT es_event (v4); COMMIT
  MySQL-->>Debezium: binlog row (commit order)
  Debezium->>Kafka: key=aggregate_id, value=row
  Kafka->>Consumer: record (partition p, offset o)
  Consumer->>Proj: eventsourcing.Event (via serializer)
  Proj->>PG: BEGIN; version check (3); VehicleProjector.Handle(tx); COMMIT
  Consumer->>Kafka: commit offset o+1 (after PG commit)
  Note over Proj,PG: crash before COMMIT → re-applied<br/>crash after COMMIT → skipped (4 ≤ 4)
```

## 5. Consistency model

- **Write side:** strongly consistent per aggregate. The CAS + unique key serialise appends, and command responses reflect committed state.
- **Read side:** **eventually consistent** and **monotonic per aggregate** (the version check means a row never moves backwards). There is no cross-aggregate snapshot consistency. Lag is exposed by `projection_lag_seconds` and the per-vehicle `version`.
- **Read-your-writes:** the client compares the `version` in the command response with the `version` on reads.
- **Aging** depends only on the immutable `stocked_on` and the request's business date, so it is never stale because of lag.

## 6. Phase 4 — as implemented (2026-10-29)

**Status:** implemented and verified on PostgreSQL 17.11 + MySQL 8.4.13. **The read model is not real-time in Phase 4.** It changes only when `sync-projections` or `rebuild-projections` runs. Live delivery via Debezium/Kafka is Phase 5 and will reuse the same `Projection.Apply`.

### 6.1 Driver and packages
- **Driver: GORM + `gorm.io/driver/postgres`** (pgx v5 underneath). The write side already uses GORM with raw SQL and `WithTransaction`; using pgx directly would add a second access abstraction for the same concern.
- **Packages:**

  | Package | Holds |
  |---|---|
  | `internal/infrastructure/viewstore` | schema and migrations, `ViewStore` write port, `Postgres` (writes + read queries) |
  | `internal/infrastructure/projection` | `Projection` pipeline, `Runner` |
  | `projection/projectors` | `VehicleProjector`, whose state transition `ProjectVehicle` is a pure function |
  | `internal/application/query` | query service and `ReadModel` port |
  | `internal/presentation` | one `*_handler.go` per query, `read_model_responses.go` (DTOs), `read_model_mappers.go`, `query_params.go` |
  | `cmd/projections.go`, `cmd/postgres.go` | projection commands, PG configuration |

### 6.2 Schema (deviations from §1)
- **IDs:** `vehicle_id TEXT`, because aggregate IDs are any client-safe string, not only UUIDs. `event_id` / `last_event_id` are `es_event.id` (BIGINT).
- **History:** `vehicle_action_history` has PRIMARY KEY `(vehicle_id, version)` and also stores `correlation_id`.
- **Applied versions:** kept in their own table, `projection_aggregate(aggregate_id, version, last_event_id)`, rather than only on `inventory_vehicle`. The pipeline owns the version check independently of any projector.
- **No `projection_checkpoint`:** there are no Kafka offsets yet. Instead `projection_run` records each sync/rebuild (kind, start/finish, events read and applied).
- **Integrity:** `CHECK ((status = 'SOLD') = (sold_on IS NOT NULL))`. Dealership reference data is seeded (`d-001` Europe/London, `d-002` Australia/Sydney), with an FK from `inventory_vehicle`.
- **No `is_aging` or `days_in_stock` column.**
- **Migrations:** versioned in `rm_schema_migrations`. Each version applies in one transaction (PostgreSQL DDL is transactional) under `pg_advisory_xact_lock`. A schema newer than the build is refused.

### 6.3 Projection correctness

`Projection.Apply(ev)` runs in one transaction:
1. **Lock:** take `pg_advisory_xact_lock_shared(projection)`, then `pg_advisory_xact_lock(aggregate)` on `hashtext(aggregate_id)`. A `SELECT … FOR UPDATE` cannot lock a row that does not exist yet; the advisory lock serialises even the **first** event of an unseen vehicle.
2. **Version check:** read the applied version (0 if none). If `version ≤ applied`, it's a duplicate and is skipped (`applied=false`). If `version ≠ applied+1`, return `ErrVersionGap`; this also covers a stream that doesn't start at 1.
3. **Schema version:** an unknown schema version gives `ErrUnsupportedSchemaVersion`.
4. **Projectors:** they run **sequentially** on the transaction-bound store. The old `errgroup` branch was removed, so no transaction is shared concurrently; the `golang.org/x/sync` dependency dropped out.
   - An unknown payload gives `ErrUnsupportedEvent`.
   - An event that doesn't fit the state (e.g. a revision of a non-active action) gives `ErrInconsistent`.
5. **Commit:** `SetAppliedVersion` writes the new version. State, history and applied version commit together; any error rolls back the whole event.

### 6.4 Sync and rebuild (Phase 4 synchronisation boundary)

`main sync-projections` / `main rebuild-projections` (`make sync-projections`, `make rebuild-projections`):

1. **Run lock:** take a PostgreSQL transaction and `pg_try_advisory_xact_lock(projection)` **exclusively**. If any run or applier holds the lock, the command fails with `ErrRunInProgress`.
2. **Rebuild only:** `TRUNCATE` the projected tables in that transaction.
3. **Read:** `repos.EventStore.ReadAll` streams **the entire history** inside ONE MySQL `START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY`, ordered by `(aggregate_id, version)` (keyset batches of 500 on the unique index).
4. **Apply** each event through the same `Projection.Apply`. Duplicates are skipped, so a sync applies only what is new.
5. **Record** the run in `projection_run` and commit. A failure anywhere rolls back everything, so the old read model stays intact.

**Why this is gap-free without a write pause:**
- No `es_event.id` cursor carries over between runs. Every run reads the full committed history as of its snapshot, so an event whose `AUTO_INCREMENT` id was allocated earlier but committed later can never be "behind the cursor".
- Within the snapshot, each stream is a contiguous prefix `1..n`, because writers of one aggregate are serialised by the `es_aggregate` CAS (Phase 2).
- Events committed after the snapshot are simply picked up by the next run.
- Verified: `TestMySQLReadAllUsesOneConsistentSnapshot` commits new events *during* a `ReadAll`, and they are not seen.

**What this is not:**
- It is not real-time and not incremental at the source: each run re-reads the whole history (O(total events)). That is fine at demo scale, and is replaced by CDC in Phase 5.

**Locking and scale:**
- Inside a run the exclusive lock already excludes every other applier. Per-aggregate advisory locks and savepoints are therefore skipped (`exclusive` mode). Thousands of transaction-scoped advisory locks would otherwise exhaust PostgreSQL's shared lock table.
- `TRUNCATE` takes an exclusive table lock, so queries wait during a rebuild and never see a partial read model.

### 6.5 Queries (`internal/application/query` + `viewstore/*_query.go`)
- **Business date:** today in the dealership's IANA zone (from the PG `dealership` table; the read side never reads MySQL), or `as_of`.
- **Ages are computed in Go** with the domain rule: `age_days`, and `is_aging = IN_STOCK ∧ age > 90`. A sold vehicle's age is `sold_on − stocked_on`.
- **Filters are sargable:**
  - `min_age_days` → `stocked_on <= bd − min`;
  - `max_age_days` → `stocked_on >= bd − max`;
  - `aging=true` → `status = 'IN_STOCK' AND stocked_on <= bd − 91`;
  - sold rows in an age filter use `sold_on − stocked_on`, only in the SOLD branch.
- **SQL text is fixed:** user input only selects among fixed predicates and two fixed `ORDER BY` clauses. Every value is a bound parameter.
- **Keyset pagination:**
  - order `(stocked_on, vehicle_id)` ASC for `age_desc` (oldest first, default), DESC for `age_asc`;
  - row-comparison predicate, limit default 20, max 100, `limit+1` fetched for `has_more`;
  - the cursor is base64url JSON `{v, s, a(as_of), f(filter fingerprint), k1, k2}`;
  - a cursor with other filters, another sort or another `as_of` → `400 INVALID_CURSOR`;
  - the cursor pins `as_of`, so paging across a dealership-local midnight stays consistent;
  - the cursor is not signed: it only selects rows the caller could request directly.
- **Indexes** were verified by `EXPLAIN` on 5,000 rows: the default, aging + cursor, and `age_asc` (backward) queries use `ix_vehicle_dealer_status_stocked`; make+model uses `ix_vehicle_dealer_make_model`; no sort nodes. The test asserts index *applicability* (`enable_seqscan=off`) and only logs the natural plan.
- **Summary:** one aggregate query with `count(*) FILTER (…)`, using the same cutoff as the list (tested equal to list counts).

### 6.6 Security
- **API:** queries use the read-only role from `deploy/postgres/grants.sql` (`make pg-grants`), which has `SELECT` on `dealership`, `inventory_vehicle`, `vehicle_action_history` and `projection_run` only. Verified: INSERT, UPDATE, DELETE, TRUNCATE, CREATE and reading `projection_aggregate` all fail with `42501`.
- **Projection commands** use the schema owner for PostgreSQL, and the restricted MySQL app user for `ReadAll` (it only needs `SELECT` on `es_event`).

### 6.7 Limitations and operational risks (accepted for Phase 4)
- **Not real-time.** A sync re-reads the full history (O(total events)) and keeps one long MySQL snapshot, which holds back InnoDB purge, plus one PostgreSQL transaction.
- **A rebuild blocks queries for its duration.** `TRUNCATE` holds an exclusive lock, there is no statement timeout on queries, and `/readyz` stays ready.
- **One bad event halts the whole run** (gap, unknown type, FK violation): the run is a single transaction. This is intended "halt, never skip" behaviour, and the operator must fix the cause.
- **Dealership reference data exists twice** (MySQL `dealership` and PostgreSQL `dealership`, both seeded). A dealership added on one side only would make syncs fail on the FK.
- **A sync cannot detect a read model that is *ahead* of MySQL** (e.g. after MySQL was reset): the old rows are skipped as duplicates. Only `rebuild-projections` repairs it.
- **Sort semantics:** `sort` orders by `stocked_on`. For in-stock vehicles that equals age; for `status=SOLD|ALL` a sold vehicle's displayed `age_days` (age at sale) is not the sort key.
- **`as_of` is a business-date override, not time travel:** it shows today's statuses, and an `as_of` before a vehicle's `stocked_on` gives a negative `age_days`.
- **The `GET` detail `ETag` is the read-model version.** Between syncs it can be behind the write side, so `If-Match` with it can return `412` until the next sync.
- **Future hazard (Phase 5):** a caller that wraps many `Apply` calls in one non-exclusive outer transaction would hold two advisory locks and a savepoint per event. The live consumer must commit per event (or per small batch).
- No `make_model` sort, no total count.
