# 06 — REST API Contracts

Per the PDF, the client layer is stubbed with this OpenAPI contract **and** runnable cURL examples (`docs/curl-examples.md`, exercised by `scripts/demo.sh`).

Base path `/v1`, JSON only. Dates are `YYYY-MM-DD`, timestamps are RFC 3339 UTC. The OpenAPI 3.1 spec lives at `api/openapi.yaml` and is served at `GET /openapi.yaml`, with Swagger UI at `/docs`. A contract test validates every handler response in the integration suite against the spec (kin-openapi).

## Common headers

| Header | Direction | Meaning |
|---|---|---|
| `X-Actor-Id` | request, **required on commands** | Stand-in for authenticated user (A8); recorded in event metadata |
| `Idempotency-Key` | request, optional on POST | Replay-safe retries (04 §4); scoped per actor |
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

**`GET /v1/dealerships/{dealershipId}/vehicles/{vehicleId}/actions`** returns the action history (B6), oldest first, keyset-paginated on `aggregate_version`.
```json
{ "data": [{ "event_id": "…", "version": 2, "action_id": "…", "change": "PROPOSED",
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
