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
