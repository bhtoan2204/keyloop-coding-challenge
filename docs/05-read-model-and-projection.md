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

## 3. CDC: Debezium → Kafka *(original plan; as implemented in §7)*

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
- **Optional (D6):** repair by reading the missing versions from MySQL with the new `repos.ReadStream` (04 / 02 §5) inside the same tx, then continue. *Implemented in Phase 5 (§7.5), one transaction per repaired event, not inside the record's transaction.*
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

**Status:** implemented and verified on PostgreSQL 17.11 + MySQL 8.4.13. In Phase 4 the read model changed only when `sync-projections` or `rebuild-projections` ran. Since Phase 5 the `projector` applies the change feed continuously through the same `Projection.Apply` (§7); sync and rebuild remain for bootstrap and recovery.

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
- **Future hazard (Phase 5):** a caller that wraps many `Apply` calls in one non-exclusive outer transaction would hold two advisory locks and a savepoint per event. The live consumer must commit per event (or per small batch). *Resolved: the Phase 5 consumer commits one transaction per event (§7.4).*
- No `make_model` sort, no total count.

## 7. Phase 5 — live change feed, as implemented (2026-10-29)

**Status:** implemented and verified on MySQL 8.4.13, Debezium 3.6.2.Final (Kafka Connect 4.3.0), Apache Kafka 4.3.1 (KRaft) and PostgreSQL 17.11. The read model follows the event store continuously; no manual sync is needed in normal operation.

```
API ──commands──► MySQL es_event ──binlog──► Debezium (Kafka Connect) ──key=aggregate_id──► Kafka topic (3 partitions)
                       ▲                                                                        │
                       │ ReadStream (gap repair), ReadAll (startup sync / data-loss resync)     ▼
                       └──────────────────────────────── projector: messaging.Consumer ──► Projection.Apply ──► PostgreSQL
```

### 7.1 Components

| Piece | Where | Notes |
|---|---|---|
| Kafka (KRaft, single node) | `docker-compose.yaml` `kafka` | `apache/kafka:4.3.1`; auto topic creation off; volume `kafka-data`; 127.0.0.1 only |
| Kafka Connect + Debezium MySQL | `docker-compose.yaml` `connect` | `quay.io/debezium/connect:3.6.2.Final`; password from `${env:DEBEZIUM_MYSQL_PASSWORD}` (EnvVarConfigProvider), never stored in the connector config |
| Connector config | `deploy/debezium/es-event-connector.json` | registered by `make connector` (idempotent PUT) |
| Connector MySQL account | `deploy/mysql/debezium-grants.sql` | `RELOAD, REPLICATION SLAVE, REPLICATION CLIENT` + `SELECT` on `es_event` only |
| Consumer core | `internal/application/messaging` | transport-neutral: decoding, partition processing, gap repair, offsets, health |
| Kafka transport | `internal/infrastructure/kafka` | franz-go v1.22.0 consumer group (`messaging.Transport`); joins the group on its first poll |
| Projection adapter | `internal/infrastructure/projection/consumer_adapter.go` | existing `Projection`, `Runner` and `repos.EventStore` behind the consumer's ports, errors translated (like `commandstore`) |
| Gap-repair read | `repos.EventStore.ReadStream` (`repos/event_stream.go`) | one aggregate's version range, decoded like `ReadAll` |
| Process | `cmd/projector.go` (`main projector`, `make run-projector`) | probes `/healthz`, `/readyz`, `/status` on `PROJECTOR_HTTP_ADDR` (`presentation/probe_router.go`); a probe server that cannot start stops the process |

The projection framework is unchanged: the consumer calls the existing `Projection.Apply`, so state, history and the applied version still commit in one PostgreSQL transaction under the shared projection lock and the per-aggregate lock (§6.3).

The Debezium envelope and key format is decoded in `application/messaging`, not in the Kafka adapter. It is the consumer's input contract, it needs no I/O, and keeping it next to the processing rules lets the default test suite cover decode → apply → commit together. The Kafka adapter only moves bytes and offsets.

### 7.2 Connector configuration (deviations from §3)

- **Full Debezium envelope, no `ExtractNewRecordState` SMT.** The consumer parses `{before, after, source, op}` itself (`messaging/debezium_envelope.go`). It sees the operation and the snapshot flag.
- **Append-only violations are emitted and halt the consumer:**
  - `skipped.operations=none` makes Debezium emit truncates (its default skips them);
  - `tombstones.on.delete=false` keeps deletes as `op=d`;
  - the decoder rejects `u`, `d`, `t` and tombstones as poison.

  Verified: `TestPipelineTruncateOfEventTableHalts` truncates `es_event` on the real stack, and the partition halts on `op "t"`.
- **`snapshot.mode=when_needed`** (plan: `initial`): snapshot on first start, and again if the stored binlog position is no longer available (binlog purged during a long outage, lost offsets topic). A re-snapshot only produces duplicates, which the version check skips.
- **Ordered snapshot:** `snapshot.select.statement.overrides` reads `es_event ORDER BY aggregate_id, version`.
- **Key:** `message.key.columns=<db>.es_event:aggregate_id` gives key `{"aggregate_id":"…"}`.
- **Converters:** JSON without schemas.
- **Heartbeat:** `heartbeat.interval.ms=10000` keeps the stored binlog position moving while no captured row changes.
- **Topic:** created by the connector as `inventory.inventory.es_event`, with 3 partitions, `cleanup.policy=delete`, 7-day retention and replication factor 1 (single broker).
- **Producer ordering:** Kafka Connect's source producers run with `acks=all` and `max.in.flight.requests.per.connection=1` (Connect defaults, read from the worker log). A retried send cannot overtake an earlier one. Idempotence is off, so a retry may write a duplicate, which the version check skips.

### 7.3 Decoding (`messaging/message_decoder.go`, `event_row_decoder.go`, `message_key.go`)

1. **Envelope:** only `op` `c` (binlog insert) or `r` (snapshot read) with an `after` row of table `es_event` is accepted.
2. **Key:** the key's `aggregate_id` must equal the row's. The key decides the partition, so a mismatch could reorder an aggregate.
3. **Row → `eventsourcing.Event`:** decoded with the **existing serializer** (`Type(aggregate_type, event_type)` + `Unmarshal`), under the same rules as `repos.decode`: the schema version must be 1, and unknown types are errors.

Golden files captured from the real connector (one snapshot record, one binlog record) are in `internal/application/messaging/testdata/`.

### 7.4 Processing, offsets and versions

- **Per-partition sequential.**
  - Each poll's records are grouped by partition; the adapter merges a partition that appears in several fetches.
  - Partitions are processed in parallel goroutines, but within a partition strictly in offset order, one `Projection.Apply` transaction per record.
  - All events of a vehicle share a partition (the key), so they are applied in version order.
- **Offsets after the PostgreSQL commit.**
  - After every partition of the batch has finished, the consumer commits, per partition, the offset after the last record whose transaction committed (`messaging/offsets.go`).
  - Then it rewinds the failed partitions, and only then calls `Release`.
  - Auto-commit is off.
  - Rebalances are blocked from a poll until `Release` (`BlockRebalanceOnPoll`), so a partition cannot move to another member between apply and commit.
- **At-least-once, idempotent by version.**
  - A crash or failed offset commit after the PostgreSQL commit re-delivers records, and `Projection.Apply` skips them (`version ≤ applied`).
  - A crash before the PostgreSQL commit rolls the transaction back, and the record is applied when re-delivered.
  - History also has `PRIMARY KEY (vehicle_id, version)`, so a duplicate can never become a second history row.
  - Exactly-once across MySQL → Kafka → PostgreSQL is **not** claimed.
- **Never skip.**
  - A failing record stops its partition at that record. The offset is not committed past it.
  - The transport rewinds to the record (`SetOffsets`) and pauses the partition for a backoff (one pending resume per partition), then retries.
  - Other partitions continue.
  - **Poison errors** retry at the maximum backoff and are logged at ERROR. Poison means a malformed record, an unexpected operation, an unknown type or schema version (from Kafka or from the event store during a gap repair), an event rejected by the projector, or an unrepairable gap.
  - **Anything else** (database or broker unavailable) backs off exponentially from 500 ms to 30 s.
- **Records lost by the feed.** franz-go resets a partition past records it never delivered when they fall below the log start (retention) or the log is truncated. It reports `ErrDataLoss`. The adapter turns that into `messaging.ErrFeedDataLoss`, and the consumer then runs a sync from MySQL before it continues.
  - Everything those records carried is committed in MySQL, so the sync applies it.
  - If the lost record was poison, the sync keeps failing and the consumer stays not ready (phase `syncing`). The skip is never silent.
  - Without a synchronizer the consumer exits with the error.
- **Readiness (`/readyz` = 503):**
  - while bootstrapping or re-syncing;
  - after shutdown has begun (phase `stopping`, while the batch in progress drains);
  - while a poll reports errors, including a failed broker connection (`kafka/dial_health.go`; the client otherwise retries silently);
  - while any owned partition is halted.

  Polls wait at most 5 s, so an idle consumer re-evaluates broker health regularly. Partition health follows the assignment callbacks: a partition revoked or lost mid-batch is not reported again by that batch.

  `/status` shows the phase, counters (applied, duplicates, gap events repaired), the last change lag, and per partition the last offset, failing offset and last error.

  Verified on the real stack: stopping the Kafka container turned `/readyz` to 503 at once, and it returned to 200 11 s after Kafka was started again.
- **Batch budget.**
  - `MaxPollRecords` is 100.
  - A read-model transaction takes milliseconds, so a batch normally ends far within the 60 s `RebalanceTimeout`.
  - On a database stalled past it, the member is evicted, its commit fails, and the records are re-delivered and skipped by version. That costs liveness, not correctness.
- **Shutdown.**
  - SIGTERM moves the consumer to `stopping`.
  - The record being applied finishes with an uncancelled context, and the processed prefix is committed.
  - The rest of the batch is re-delivered to the next owner.
  - The consumer leaves the group.

### 7.5 Bootstrap strategy (snapshot, existing history, existing projections)

The read model may already hold projections, and MySQL may hold any amount of history. Three mechanisms cover different holes. All are idempotent by version.

1. **Startup sync (`PROJECTOR_STARTUP_SYNC=true`, default).**
   - Before the transport joins the group (the client is created on the first poll), the consumer runs `Runner.Sync`.
   - Under the *exclusive* projection lock, the sync reads every committed event from ONE consistent MySQL snapshot `S` and applies what is missing.
   - Everything committed before startup is therefore in PostgreSQL before any Kafka record is processed, whatever the topic still holds.
   - The sync is retried with backoff while another applier holds the lock.
2. **Debezium snapshot + binlog.**
   - On first registration the connector snapshots `es_event`, ordered per aggregate.
   - It then streams the binlog from the position recorded with the snapshot, under the snapshot's global read lock (`snapshot.locking.mode=minimal`). Every row is either in the snapshot or after that binlog position.
   - Snapshot records are produced before streamed ones, and both kinds of one aggregate go to the same partition. Per-aggregate order therefore holds across the snapshot-to-streaming boundary.
   - Rows already projected arrive as duplicates and are skipped.
3. **Version-based gap repair.**
   - If a record's version is not `applied + 1`, the consumer reads versions `1 … v−1` of that aggregate from MySQL (`ReadStream`) and applies them in order, then the record. This covers an earlier version missing from the feed or arriving later.
   - Records that arrive later are duplicates.
   - Once version `v` is committed, every lower version is committed too (the version CAS serialises the writers of an aggregate). The read is therefore complete unless history is broken; if it is broken, the gap is **unrepairable** and the partition halts.

Data-loss resync (§7.4) is the same sync as mechanism 1, run while consuming.

**Why nothing is missed or reordered.**
- Ordering never relies on `es_event.id` (AUTO_INCREMENT ids are allocated at insert, not at commit). It relies on per-aggregate versions, the per-aggregate partition key, and Debezium's snapshot/binlog handoff.
- The invariant that holds for any number of members and restarts: **every committed Kafka offset belongs to a record whose event is in this PostgreSQL database.** Offsets are committed only after the read-model commit; a rebuild re-applies them (see below).
- Take any event `E`:
  - If `E` committed before the latest startup sync's snapshot `S`, that sync applied it.
  - Otherwise `E` is in the binlog after the connector's recorded position. Its record sits at or after its partition's committed offset, unless it was already applied, so some member delivers it. If retention deletes it first, the data-loss resync applies it.
  - Duplicates in any of these paths are skipped by version.
  - An out-of-order arrival is fixed by gap repair, or halts if MySQL disagrees.

**Rebuild while the consumer runs.**
- The consumer's `Apply` takes the projection lock *shared*. `rebuild-projections` takes it *exclusively* before it opens its MySQL snapshot `S`.
- Every event the consumer applied before the rebuild committed in MySQL before the lock, so it is in `S` and re-applied by the rebuild.
- Events after `S` wait for the rebuild's commit and are then applied on top. Their offsets were not committed yet, so nothing is lost.
- A rebuild fails fast (`ErrRunInProgress`) if it cannot get the lock at that instant, and is retried.

**Caveats.**
- **A read model restored or replaced outside these commands** (for example a PostgreSQL restore from backup) while the projector keeps its offsets: aggregates with no later event stay stale until a projector restart (startup sync) or `make sync-projections`.
- **With `PROJECTOR_STARTUP_SYNC=false`,** correctness after downtime relies on the topic still holding the records, the data-loss resync, or gap repair. Keep it enabled in production; disabling it is meant for tests and demos.
- **Adding partitions** changes key → partition. Old and new events of an aggregate can then sit on different partitions and arrive out of order. Gap repair and duplicate skipping keep the result correct, but in-order delivery is lost. Do not change the partition count casually.

| Situation | What happens | Verified by |
|---|---|---|
| Existing history, no projections | startup sync and/or Debezium snapshot apply it | `TestPipelineInitialSnapshotWithExistingHistoryAndProjections` (Debezium snapshot only, startup sync off) |
| Existing projections | snapshot / re-delivered records skipped as duplicates | same test: 18 skipped, 18 applied; E2E run: 12 snapshot records skipped |
| Commands written during connector snapshot and consumer bootstrap | snapshot/binlog handoff + startup sync | `TestPipelineBootstrapWhileCommandsAreWritten` |
| Consumer restart, crash after PG commit before offset commit | re-delivered, skipped | `TestPipelineConsumerCrashAndRestart`, `TestCrashAfterProjectionCommitBeforeOffsetCommit` |
| Crash / failure before PG commit | rolled back, re-applied once | `TestPipelineProjectionFailureRollsBackAndHalts`, `TestCrashBeforeProjectionCommit` |
| Connector restart, pause, delete + re-create | resumes from stored binlog offset; replays skipped | `TestPipelineConnectorRestart` |
| Rebuild during live consumption | lock ordering as above | `TestPipelineRebuildDuringLiveConsumption`, E2E step 14 |
| Version arrives early / feed starts mid-stream | gap repaired from MySQL | `TestOutOfOrderVersionsRepairedFromEventStore`, `TestFeedStartingMidStreamRepairedFromEventStore` |
| Version missing from MySQL too | partition halts, not ready | `TestUnrepairableGapHaltsOnlyItsPartition` |
| Records deleted before being consumed | resync from MySQL | `TestFeedDataLossResyncsFromEventStore` (fake transport; not reproduced on Kafka) |
| `es_event` truncated | partition halts on `op=t` | `TestPipelineTruncateOfEventTableHalts` |

### 7.6 Failure handling and recovery runbook

| Symptom (`/readyz` 503, `/status`) | Cause | Recovery |
|---|---|---|
| `phase: syncing` for long | another sync/rebuild holds the projection lock, MySQL/PG unavailable, or (after data loss) an event the sync cannot apply | check the log (`sync from the event store failed`); restore the database or fix the event as in the poison row |
| `transport_error` set | broker unreachable or topic missing | start Kafka / register the connector (`make cdc-up connector`); the consumer reconnects and becomes ready again |
| partition `failing`, not `poison` | PostgreSQL or MySQL errors (timeouts, FK violation for an unknown dealership) | fix the dependency or data (e.g. add the dealership to both reference tables); the record is retried automatically |
| partition `failing`, `poison` | malformed record, `u`/`d`/`t` on `es_event`, unknown event type or schema version, event rejected by the projector, unrepairable gap | **Do not skip.** Deploy a build that understands the event (new type or upcaster), or repair the event store (history must stay append-only and contiguous). Then restart the projector: the startup sync re-applies from MySQL and the record is retried. After a truncate or restore of `es_event`, rebuild the read model and recreate the connector and consumer group, because the Kafka history no longer matches MySQL. |
| read model suspected stale or wrong | any | `make sync-projections` (adds what is missing; safe while the projector runs) or `make rebuild-projections` (replaces it) |
| connector `FAILED` | MySQL privileges, unreachable MySQL or Kafka | `make connector-status`, fix, `make connector-restart`; a re-snapshot only causes duplicates |

### 7.7 Verification (this session)

- **Unit tests** (default suite; fake transport and projector) cover:
  - decoding: golden snapshot and binlog records, every rejection class (including a key-less truncate);
  - delivery: duplicates, crashes before and after the PostgreSQL commit, restart, transient retry in place;
  - gaps: out-of-order versions, a feed starting mid-stream, an unrepairable gap halting only its partition, repair counted when the record still fails;
  - a poison record is never skipped;
  - startup sync, data-loss resync, and data loss without a synchronizer;
  - transport errors clearing after a healthy poll;
  - the batch cycle order poll → commit → retry → release;
  - shutdown letting the apply in progress finish;
  - a lost partition not resurrected by its batch;
  - readiness and backoff.

  Also tested: the adapter's error translation (projection and event store), `ReadStream` on SQLite and MySQL, the probe handler, the Kafka config, `Transport` use before connecting and after close, and the batch merge.
- **Pipeline tests** (`make test-pipeline`, real MySQL + Debezium + Kafka + PostgreSQL):
  - each test has its own databases, connector, topic and group;
  - 9 tests, 2 consecutive green runs after the review fixes;
  - each convergence test compares the live read model with a rebuild from MySQL and checks that history rows equal non-stock events.
- **Mutation checks:** 19 mutants, all killed.
  - Skipping a failed record.
  - Not rewinding a failed partition.
  - Disabling gap repair; accepting a partial repair.
  - Dropping the key/row check; accepting `op=u`; dropping the schema-version check.
  - Skipping the startup sync.
  - Committing a failed record.
  - A Kafka `Retry` without `SetOffsets`; a Kafka `Commit` that commits nothing.
  - No resync on data loss.
  - A lost partition re-created by its batch.
  - An apply cancelled by shutdown; no `stopping` phase.
  - An unbounded poll.
  - `Release` before commit.
  - A repair count dropped on failure.
  - Batch merging disabled.
- **End to end** with the built binary (`api` + `projector`, Compose stack):
  - the stocked vehicle was visible through `GET` 363 ms after the command returned, with no sync;
  - over 20 further commands, command return → row visible through the API: min 376 ms, median 478 ms, p90 490 ms, max 493 ms (single host, WSL2 + rootless Podman; hypothesis, unverified: the floor is Debezium's default `poll.interval.ms` of 500 ms);
  - stopping the projector, writing 4 commands and restarting it without startup sync applied exactly those 4 events (0 duplicates), giving 5 history rows for 5 versions;
  - `rebuild-projections` while the projector ran produced identical rows (21/21);
  - a Kafka outage turned `/readyz` to 503 and recovered.
- **Independent reviews** (read-only Event Sourcing and Go reviewer agents): no Critical findings. The fixes are listed in `ai-log.md`.

### 7.8 Limitations and risks

- **Local stand-in, not production durability:** single broker, replication factor 1, one Connect worker.
- **Startup sync cost:** O(total events) per projector start, holding the exclusive projection lock. While it runs, other members' applies wait and may time out and retry. Fine at this scale; at scale, replace it with a checkpointed catch-up.
- **Gap repair reads the whole stream prefix** (`1 … v−1`) of one aggregate; vehicle streams are short.
- **A halted partition holds back every aggregate on it,** not only the failing one (strict per-partition order).
- **Offsets live only in Kafka** (D5 not implemented). The version check makes re-reads harmless.
- **Read-your-writes** is still by version comparison (§5). Typical lag on this machine is about 0.5 s.
- **The connector account needs global `RELOAD` and replication privileges** (MySQL has no narrower grant for binlog reading).
- **Kafka, Connect and the projector's `/status`** (which shows raw error strings) are plaintext and unauthenticated, bound to 127.0.0.1 only.
- **`make debezium-grants` substitutes the password into SQL with `sed`,** as `make mysql-grants` already does. A password containing `/`, `&` or `'` breaks it, so use alphanumeric passwords.
- **Data-loss resync is verified with a fake transport only.** Kafka retention expiry was not reproduced on the real stack.
