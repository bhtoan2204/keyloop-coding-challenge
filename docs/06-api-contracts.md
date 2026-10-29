# 06 — REST API Contracts

Per the PDF, the client layer is stubbed with this OpenAPI contract **and** runnable cURL examples (`docs/curl-examples.md`, exercised by `scripts/demo.sh`).

Base path `/v1`, JSON only. Dates are `YYYY-MM-DD`, timestamps are RFC 3339 UTC. The OpenAPI 3.1 spec lives at `api/openapi.yaml` and is served at `GET /openapi.yaml`, with Swagger UI at `/docs`. A contract test validates every handler response in the integration suite against the spec (kin-openapi).

## Common headers

| Header | Direction | Meaning |
|---|---|---|
| `X-Actor-Id` | request, **required on commands** | Stand-in for authenticated user (A8); recorded in event metadata |
| `Idempotency-Key` | request, optional on POST | Replay-safe retries; scoped per actor. **Implemented only if time allows** (04 §6, cut-list item 3). Otherwise it is ignored. |
| `If-Match: "<version>"` | request, optional on action/sale commands | Rejects with 412 if the aggregate version differs |
| `ETag: "<version>"` | response on vehicle resources | Aggregate version (from the write side on commands, the read model on reads) |
| `X-Request-Id` / `traceparent` | both | Correlation; generated if absent |
| `Idempotent-Replayed: true` | response | The response is a stored replay |

## Endpoints

### Queries (read model, Postgres)

**`GET /v1/dealerships/{dealershipId}/vehicles`** lists inventory (B1, B2, B4).

| Param | Type | Default | Notes |
|---|---|---|---|
| `make`, `model` | string | — | exact, case-insensitive |
| `min_age_days`, `max_age_days` | int ≥ 0 | — | inclusive; `min ≤ max` else 400 |
| `aging` | bool | — | `true` ⇒ in stock and age > 90 |
| `status` | `IN_STOCK` \| `SOLD` \| `ALL` | `IN_STOCK` | |
| `sort` | `age_desc` \| `age_asc` \| `make_model` | `age_desc` | |
| `limit` | int 1..100 | 20 | |
| `cursor` | opaque | — | from `page.next_cursor`; incompatible filters → 400 |
| `as_of` | date | dealership today | business-date override (additional; demo/testing) |

`200`:
```json
{
  "as_of": "2026-10-29",
  "aging_threshold_days": 90,
  "data": [{
    "vehicle_id": "0192c6d2-…",
    "vin": "1HGCM82633A004352",
    "make": "Toyota", "model": "Corolla", "model_year": 2023,
    "stocked_on": "2026-06-01",
    "status": "IN_STOCK", "sold_on": null,
    "age_days": 150, "is_aging": true,
    "current_action": {
      "action_id": "0192c6e0-…", "action_type": "PRICE_REDUCTION",
      "note": "Reduce by 5%", "proposed_at": "2026-10-20T09:12:00Z",
      "updated_at": "2026-10-21T10:00:00Z", "updated_by": "manager-42"
    },
    "version": 3
  }],
  "page": { "limit": 20, "has_more": true, "next_cursor": "eyJ…" }
}
```
Errors: `400 VALIDATION_ERROR | INVALID_CURSOR`, `404 DEALERSHIP_NOT_FOUND`.

**`GET /v1/dealerships/{dealershipId}/inventory/summary`** returns dashboard tiles for "prominently display aging stock" (B4, D10). Accepts `as_of`.
```json
{ "as_of": "2026-10-29", "aging_threshold_days": 90,
  "in_stock": 58, "aging": 14, "aging_without_action": 9,
  "oldest_stocked_on": "2026-03-02" }
```
One aggregate query over `ix_vehicle_dealer_status_stocked`: `count(*) FILTER (WHERE stocked_on < $bd - 90)`, and the same with `AND action_id IS NULL`.

**`GET /v1/dealerships/{dealershipId}/vehicles/{vehicleId}`** returns one vehicle (same item shape) plus `ETag`. Returns `404 VEHICLE_NOT_FOUND`, including when the vehicle belongs to another dealership.

**`GET /v1/dealerships/{dealershipId}/vehicles/{vehicleId}/actions`** returns the action history (B6), oldest first, keyset-paginated on `version`. `event_id` is `es_event.id`, and `occurred_at` is derived from `es_event.created_at` (unix seconds).
```json
{ "data": [{ "event_id": 1042, "version": 2, "action_id": "…", "change": "PROPOSED",
             "action_type": "PRICE_REDUCTION", "note": "…", "reason": null,
             "actor_id": "manager-42", "occurred_at": "…" }],
  "page": { "limit": 20, "has_more": false, "next_cursor": null } }
```

### Commands (write side, MySQL event store)

All commands respond with the **post-command state computed from the aggregate**, plus `version`, so callers never have to wait for the projection.

| Method & path | Body | Success | Domain errors |
|---|---|---|---|
| `POST /v1/dealerships/{d}/vehicles` | `{vehicle_id?, vin, make, model, model_year, stocked_on}` | `201` + `Location` | `409 VEHICLE_ALREADY_EXISTS`, `409 VIN_ALREADY_IN_STOCK` |
| `POST /v1/dealerships/{d}/vehicles/{v}/actions` | `{action_type, note}` | `201` (`action_id` generated) | `409 VEHICLE_NOT_AGING`, `409 ACTION_ALREADY_ACTIVE`, `409 VEHICLE_NOT_IN_STOCK` |
| `PUT /v1/dealerships/{d}/vehicles/{v}/actions/{actionId}` | `{action_type, note}` | `200` (no-op if unchanged) | `409 ACTION_NOT_ACTIVE`, `409 VEHICLE_NOT_IN_STOCK` |
| `POST /v1/dealerships/{d}/vehicles/{v}/actions/{actionId}/cancel` | `{reason?}` | `200` | `409 ACTION_NOT_ACTIVE` |
| `POST /v1/dealerships/{d}/vehicles/{v}/sale` | `{sold_on}` | `200` | `409 VEHICLE_ALREADY_SOLD`, `400` date rules |

All command endpoints may also return: `400 VALIDATION_ERROR`, `404 VEHICLE_NOT_FOUND | DEALERSHIP_NOT_FOUND`, `409 CONCURRENCY_CONFLICT` (retries exhausted), `412 VERSION_MISMATCH` (If-Match), `422 IDEMPOTENCY_KEY_REUSED`, `503 UNAVAILABLE`.

`PUT` (not `PATCH`) is used for revise because the body is the full new value of the action. Cancel and sale are `POST` sub-resources because they are business operations, not field updates.

### Operational

| Path | Purpose |
|---|---|
| `GET /healthz` | liveness (process up) |
| `GET /readyz` | readiness: MySQL + Postgres ping (api); Kafka + Postgres + not halted (projector) |
| `GET /metrics` | Prometheus (both processes, separate ports) |
| `GET /openapi.yaml`, `/docs` | spec, Swagger UI |

## Error format (RFC 9457 `application/problem+json`)

```json
{
  "type": "https://errors.inventory.local/VEHICLE_NOT_AGING",
  "title": "Vehicle is not aging",
  "status": 409,
  "code": "VEHICLE_NOT_AGING",
  "detail": "Vehicle 0192c6d2-… is 45 days in stock; actions require more than 90.",
  "instance": "/v1/dealerships/d-001/vehicles/0192c6d2-…/actions",
  "request_id": "…",
  "errors": [{ "field": "note", "message": "must be at most 1000 characters" }]
}
```

Domain errors are typed sentinels in `domain`. They are mapped to HTTP **only** in the HTTP layer, through one table. Unexpected errors return `500` with no internal detail in the body. Full detail and the trace ID go to the log.

## Phase 3 — write API as implemented (2026-10-29)

Implemented in `internal/presentation` (stdlib `net/http` routing) on top of `internal/application/command`. Query endpoints (GET list, detail, history, summary) come with the PostgreSQL read model in the next phase.

| Method & path | Success | Notes |
|---|---|---|
| `POST /v1/dealerships/{d}/vehicles` | `201`, `Location: /v1/dealerships/{d}/vehicles/{id}`, `ETag: "1"` | Body `{vehicle_id?, vin, make, model, model_year, stocked_on}`. `vehicle_id` is optional (UUIDv7 generated), 1–128 chars `[A-Za-z0-9._:@-]`. The `Location` target (GET) arrives with the read model. |
| `POST …/vehicles/{v}/actions` | `201`, `Location: …/actions/{actionId}`, `ETag` | Body `{action_type, note}`. `action_id` is generated and returned in `current_action`. |
| `PUT …/vehicles/{v}/actions/{actionId}` | `200` + `ETag` | Identical values are a no-op: `200`, same version, no event |
| `POST …/vehicles/{v}/actions/{actionId}/cancel` | `200` + `ETag` | Body `{reason?}`. An empty body is allowed. |
| `POST …/vehicles/{v}/sale` | `200` + `ETag` | Body `{sold_on}` |
| `GET /healthz` / `GET /readyz` | `200` / `200` or `503` | Readiness pings MySQL (2 s timeout) and doesn't reveal why it failed. |

### Response body

Every command responds with the **committed write-side state** of the aggregate. It is built from the aggregate only after `SaveWithMetadata` returned without error, so a failed save never produces a success body. Fields:

```
vehicle_id, dealership_id, vin, make, model, model_year, stocked_on, status, sold_on|null,
age_days, is_aging, current_action{action_id, action_type, note}|null, version, business_date
```

`age_days` and `is_aging` are derived at `business_date` (the dealership's date from its IANA zone) and never stored.

Deviation from the read-model shape above: no `proposed_at`, `updated_at` or `updated_by` timestamps. The aggregate doesn't hold them; the read model will.

### Request handling

- **Bodies:** `Content-Type: application/json` is required (otherwise `415`). Bodies are capped at 64 KiB (`413`). Unknown fields are rejected, so a `dealership_id` in the body is a `400 INVALID_BODY`: the dealership always comes from the URL. Wrong types and trailing data are also rejected.
- **`X-Actor-Id` is required on every command** (`400 ACTOR_REQUIRED` / `INVALID_ACTOR`). **It is an unauthenticated, demo-only identity:** it is recorded as `actor_id` in event metadata for audit and is never used for authorisation. Real authentication is out of scope.
- **`X-Request-Id`:** a valid inbound value is kept, otherwise a UUIDv7 is generated. It is echoed in the response, used as `correlation_id` in event metadata, and included in problem bodies and logs. A valid W3C `traceparent` is stored in metadata too.
- **`If-Match: "<version>"`** (strong ETag only; `W/` → `400 INVALID_IF_MATCH`). A mismatch returns `412 VERSION_MISMATCH` and is never retried with a newer version.

### Error codes added by the implementation

| Status | Code | When |
|---|---|---|
| 400 | `ACTOR_REQUIRED`, `INVALID_ACTOR`, `INVALID_BODY`, `INVALID_IF_MATCH`, `VALIDATION_ERROR` (with `errors[{field,message}]`) | request problems |
| 404 | `DEALERSHIP_NOT_FOUND`, `VEHICLE_NOT_FOUND`, `NOT_FOUND` | `VEHICLE_NOT_FOUND` also covers a vehicle of another dealership (no cross-tenant probing) |
| 409 | `VEHICLE_ALREADY_EXISTS`, `VEHICLE_NOT_IN_STOCK`, `VEHICLE_ALREADY_SOLD`, `VEHICLE_NOT_AGING`, `ACTION_ALREADY_ACTIVE`, `ACTION_NOT_ACTIVE` | domain rules |
| 409 | `CONCURRENCY_CONFLICT` | optimistic conflicts on all 3 attempts |
| 412 | `VERSION_MISMATCH` | stale `If-Match` |
| 413 / 415 | `BODY_TOO_LARGE` / `UNSUPPORTED_MEDIA_TYPE` | |
| 503 | `UNAVAILABLE` + `Retry-After: 1` | database unreachable or connection lost, lock wait timeouts or deadlocks on every attempt, deadline exceeded. **Outcome unknown:** check the vehicle's version before retrying. |
| 500 | `INTERNAL` | anything else, with a generic detail. The full error goes only to the log. |

### Retries and idempotency

- **Server-side retry:** on an optimistic conflict, deadlock or lock wait timeout, the command service reloads the vehicle and re-runs the command on the fresh state. That's at most 3 attempts, stopping on context cancellation. The domain rule therefore decides the outcome: concurrent proposals give one `201` and `409 ACTION_ALREADY_ACTIVE` for the rest.
- **Never retried:** domain rejections, `If-Match` requests, and connection failures, where the commit may or may not have happened.
- **Limitation, no `Idempotency-Key` yet:** a client that retries after a timeout or a `503` may repeat a command that did commit. Some repeats are caught by domain rules:
  - stock with the same `vehicle_id` → 409;
  - propose again → 409;
  - sell again → 409;
  - identical revise → no-op.

  But a non-identical revise, or a cancel then re-propose, could apply twice. Clients should send `If-Match` (or re-read the vehicle) before retrying.
