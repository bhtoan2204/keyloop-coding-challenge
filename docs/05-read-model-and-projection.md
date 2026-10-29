# 05 — Read Model, CDC and Projection

## 1. PostgreSQL schema

```sql
CREATE TABLE dealership (                         -- reference data (same seed as MySQL)
  dealership_id TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  timezone      TEXT NOT NULL
);

CREATE TABLE inventory_vehicle (
  vehicle_id        UUID PRIMARY KEY,
  dealership_id     TEXT     NOT NULL REFERENCES dealership,
  vin               CHAR(17) NOT NULL,
  make              TEXT     NOT NULL,
  model             TEXT     NOT NULL,
  model_year        SMALLINT NOT NULL,
  stocked_on        DATE     NOT NULL,
  status            TEXT     NOT NULL CHECK (status IN ('IN_STOCK','SOLD')),
  sold_on           DATE,
  -- current action (denormalised for the list view; NULL = none)
  action_id         UUID,
  action_type       TEXT,
  action_note       TEXT,
  action_proposed_at TIMESTAMPTZ,
  action_updated_at  TIMESTAMPTZ,
  action_updated_by  TEXT,
  -- projection bookkeeping
  version           INT         NOT NULL,      -- last applied aggregate_version
  last_event_id     UUID        NOT NULL,
  updated_at        TIMESTAMPTZ NOT NULL
);

CREATE TABLE vehicle_action_history (
  event_id          UUID PRIMARY KEY,
  vehicle_id        UUID NOT NULL REFERENCES inventory_vehicle ON DELETE CASCADE,
  aggregate_version INT  NOT NULL,
  action_id         UUID NOT NULL,
  change            TEXT NOT NULL CHECK (change IN ('PROPOSED','REVISED','CANCELLED','CLOSED_BY_SALE')),
  action_type       TEXT NOT NULL,
  note              TEXT,
  reason            TEXT,
  actor_id          TEXT NOT NULL,
  occurred_at       TIMESTAMPTZ NOT NULL,
  UNIQUE (vehicle_id, aggregate_version)
);

CREATE TABLE projection_checkpoint (
  projection  TEXT    NOT NULL,         -- 'inventory_v1'
  topic       TEXT    NOT NULL,
  partition   INT     NOT NULL,
  next_offset BIGINT  NOT NULL,
  updated_at  TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (projection, topic, partition)
);
```

There is deliberately **no `days_in_stock` or `is_aging` column**. Both are computed at query time (§2).

### Indexes

Reads are always single-dealership and mostly `status='IN_STOCK'`. Per-dealership cardinality is small (A12), so the indexes target the *default* list and the most selective filter, not every combination.

```sql
-- default list, age filters, aging-only, sort by age (both directions via backward scan)
CREATE INDEX ix_vehicle_dealer_status_stocked
  ON inventory_vehicle (dealership_id, status, stocked_on, vehicle_id);

-- make / make+model filters and make_model sort
CREATE INDEX ix_vehicle_dealer_make_model
  ON inventory_vehicle (dealership_id, status, lower(make), lower(model), stocked_on, vehicle_id);

-- history by vehicle is covered by UNIQUE (vehicle_id, aggregate_version)
```

Every index is checked with `EXPLAIN` in an integration test on a seeded dataset (~10k rows). The test asserts an Index Scan and no Seq Scan for the default and aging queries.

## 2. Query-time aging

The application resolves `business_date` = today in the dealership's time zone (or `as_of` if supplied), then:

```sql
SELECT v.*, ($bd::date - v.stocked_on)                                   AS age_days,
       (v.status = 'IN_STOCK' AND v.stocked_on < $bd::date - 90)        AS is_aging
FROM inventory_vehicle v
WHERE v.dealership_id = $dealer
  AND v.status = ANY($statuses)                        -- default {IN_STOCK}
  AND ($make  IS NULL OR lower(v.make)  = lower($make))
  AND ($model IS NULL OR lower(v.model) = lower($model))
  AND ($min_age IS NULL OR v.stocked_on <= $bd::date - $min_age)   -- age >= min
  AND ($max_age IS NULL OR v.stocked_on >= $bd::date - $max_age)   -- age <= max
  AND ($aging   IS NOT TRUE OR (v.status='IN_STOCK' AND v.stocked_on < $bd::date - 90))
  AND (keyset predicate)
ORDER BY v.stocked_on ASC, v.vehicle_id ASC
LIMIT $limit + 1;                                       -- +1 to detect has_more
```

Age predicates are rewritten as range predicates on `stocked_on`, so they stay sargable (index range scan). `age_days > 90 ⇔ stocked_on < bd − 90`, so exactly 90 days is not aging. For sold vehicles the API reports `age_days = sold_on − stocked_on`. In production the SQL builder emits only the predicates that are actually present, rather than `$x IS NULL OR …`, so the planner sees a stable shape.

### Sorting & keyset pagination

| `sort` | ORDER BY | Keyset tuple |
|---|---|---|
| `age_desc` (default, oldest first) | `stocked_on ASC, vehicle_id ASC` | `(stocked_on, vehicle_id) > ($1,$2)` |
| `age_asc` | `stocked_on DESC, vehicle_id DESC` | `(stocked_on, vehicle_id) < ($1,$2)` |
| `make_model` | `lower(make), lower(model), stocked_on, vehicle_id` | row comparison on 4 columns |

The cursor is base64url JSON `{sort, last: [...], as_of, filter_hash}`, HMAC-signed. It **pins `as_of`**, so paging across midnight stays consistent. A cursor reused with different filters returns `400 INVALID_CURSOR`. `vehicle_id` is the unique tie-breaker. The limit defaults to 20 and must be in 1..100, otherwise `400`.

## 3. CDC: Debezium → Kafka

Connector (registered by a one-shot container at startup):

```json
{
  "connector.class": "io.debezium.connector.mysql.MySqlConnector",
  "topic.prefix": "inventory",
  "database.include.list": "inventory",
  "table.include.list": "inventory.event_store",
  "message.key.columns": "inventory.event_store:aggregate_id",
  "snapshot.mode": "initial",
  "include.schema.changes": "false",
  "schema.history.internal.kafka.topic": "inventory.schema-history",
  "transforms": "unwrap",
  "transforms.unwrap.type": "io.debezium.transforms.ExtractNewRecordState",
  "transforms.unwrap.add.fields": "op,source.file,source.pos",
  "transforms.unwrap.drop.tombstones": "true",
  "key.converter": "org.apache.kafka.connect.json.JsonConverter",
  "key.converter.schemas.enable": "false",
  "value.converter": "org.apache.kafka.connect.json.JsonConverter",
  "value.converter.schemas.enable": "false"
}
```

- Only `event_store` is captured. `vin_reservation`, `idempotency_key` and `dealership` never leave MySQL.
- **Key = `aggregate_id`**: all events of a vehicle go to the same partition in commit order (02 R1). Topic `inventory.inventory.event_store`, 3 partitions (broker `num.partitions=3`).
- The projector accepts only `__op ∈ {c, r}` (insert, snapshot read). Anything else is a hard error, which cannot happen thanks to the grants and triggers.
- Delivery is **at-least-once** end to end. Connect can re-publish after a crash, and the consumer can re-read after a crash. Nothing below assumes exactly-once across MySQL → Kafka → Postgres.

## 4. Projector

### 4.1 Processing one record (one Postgres transaction)

```
BEGIN
  SELECT version FROM inventory_vehicle WHERE vehicle_id=$id FOR UPDATE      -- 0 if absent
  if  ev.version <= current     → duplicate: skip state change          (metric duplicates_total)
  elif ev.version == current+1  → apply(ev)
  else                          → gap: repair (§4.3), then apply(ev)       (metric gaps_total)
  UPSERT projection_checkpoint (projection, topic, partition, next_offset = record.offset+1)
COMMIT
then: best-effort Kafka offset commit (only for lag visibility in Kafka tooling)
```

- `apply(ev)` updates `inventory_vehicle` (including `version` and `last_event_id`) and inserts into `vehicle_action_history`, in the same tx.
- **State, per-aggregate version and the Kafka offset are committed atomically** (D5). A crash at any point either loses the whole tx (the record is re-read and re-applied) or keeps all of it (the record is skipped next time). The duplicate check makes the re-read harmless anyway. The offset checkpoint just avoids re-reading.
- `FOR UPDATE` on the vehicle row serialises the live consumer and gap repair or rebuild on the same aggregate.

### 4.2 Ordering & concurrency

Kafka guarantees order within a partition, and the key guarantees that a vehicle lives in one partition. The consumer processes **each partition sequentially**. Partitions may run in parallel, one goroutine each. On partition assignment (`OnPartitionsAssigned`) the consumer reads `projection_checkpoint` and seeks to `next_offset`. With no row it starts from the earliest offset, which is safe because the apply is idempotent.

### 4.3 Gaps and out-of-order versions

- `ev.version <= current` means a duplicate or stale redelivery. It is ignored, and the offset is still advanced.
- `ev.version > current + 1` means missing events. Causes include connector misconfiguration, a lost Kafka segment, or an offset reset after retention expiry. The projector **reads versions `current+1 .. ev.version-1` from MySQL** (`ReadStream`, read-only user), applies them in order, then applies `ev`, all in the same tx. MySQL is the source of truth, so this is self-healing (D6).
- If MySQL does not have the missing versions either (impossible unless the write side is corrupt), the tx is rolled back and the partition is paused with exponential backoff. An `ERROR` log with aggregate and version context is written, `projection_halted{partition}` is set to 1, and readiness turns false. Records are **never skipped**, because that would make the read model silently wrong.
- Unknown `event_type`/`schema_version` is handled the same way: halt the partition, never skip (02 R9).

### 4.4 Rebuild (does not depend on Kafka retention)

`inventory rebuild`:
1. Take `pg_advisory_lock(<projection key>)`. The running projector holds the same lock for its whole session, so a rebuild cannot run concurrently with a live projector. The command fails fast with "stop the projector first".
2. `TRUNCATE inventory_vehicle, vehicle_action_history` in one tx. **Do not** truncate `projection_checkpoint`.
3. Stream `event_store` from MySQL ordered by `global_position` in batches of 1,000 (keyset on `global_position`), using the same `apply` code path. Each batch commits in its own Postgres tx.
4. Release the lock. Restart the projector.

Why this is correct: the projector resumes at its stored Kafka offsets. Every event committed after it stopped is at or after those offsets. Events that the rebuild already applied are skipped as duplicates by version, and newer ones are applied. If those offsets are no longer in Kafka (retention expired), the consumer resets to earliest and gap repair fills in from MySQL. Either way, MySQL alone is sufficient.

Limitation: during a rebuild the read model is partially populated. That is acceptable here and documented. Production would use blue/green: build into `inventory_v2` tables and swap a view.

### 4.5 Sequence

```mermaid
sequenceDiagram
  participant API
  participant MySQL
  participant Debezium
  participant Kafka
  participant Projector
  participant PG as Postgres
  API->>MySQL: BEGIN; INSERT event_store (v4); COMMIT
  API-->>API: 200 {state, version: 4}
  MySQL-->>Debezium: binlog row (commit order)
  Debezium->>Kafka: key=aggregate_id, value=row
  Kafka->>Projector: record (partition p, offset o)
  Projector->>PG: BEGIN; check version=3; apply v4; checkpoint(p,o+1); COMMIT
  Note over Projector,PG: crash before COMMIT → record re-read, re-applied<br/>crash after COMMIT → record skipped (version 4 ≤ 4)
```

## 5. Consistency model

- **Write side**: strongly consistent per aggregate. Appends are linearisable through the unique `(aggregate_id, aggregate_version)`. Command responses reflect the committed write-side state.
- **Read side**: **eventually consistent**, **monotonic per aggregate** (a vehicle's row never moves to an older version), and with **no cross-aggregate snapshot consistency** (a list page may mix vehicles at different lag). Typical lag is sub-second. It is exposed as `projection_lag_seconds` (now − `occurred_at` of the last applied event) and is visible per vehicle via the `version` field.
- **Read-your-writes**: the client compares the `version` from the command response with `version` on reads. No server-side waiting is implemented. A `min_version` hint is listed as an extension.
- **Aging** depends only on `stocked_on` (immutable) and the request's business date, so it is never stale due to lag.
