# curl examples

The contract is [`api/openapi.yaml`](../api/openapi.yaml). A running API also serves it at
`http://127.0.0.1:8080/openapi.yaml` and as Swagger UI at `http://127.0.0.1:8080/docs`.

The examples assume the stack from the README (`make stack-up`) and `jq` for readability.

```bash
API=http://127.0.0.1:8080
D=$API/v1/dealerships/d-001          # seeded dealerships: d-001 (Europe/London), d-002 (Australia/Sydney)
ACTOR='X-Actor-Id: manager-42'       # unauthenticated, demo-only identity; required on commands
JSON='Content-Type: application/json'
```

## Commands (write side, strongly consistent)

### Stock a vehicle

`vehicle_id` is optional; a UUIDv7 is generated without it.

```bash
curl -s -i -X POST $D/vehicles -H "$ACTOR" -H "$JSON" -d '{
  "vehicle_id": "veh-1001",
  "vin": "1HGCM82633A004352",
  "make": "Toyota",
  "model": "Corolla",
  "model_year": 2023,
  "stocked_on": "2026-06-01"
}'
```

```
HTTP/1.1 201 Created
Etag: "1"
Location: /v1/dealerships/d-001/vehicles/veh-1001
X-Request-Id: 01a18702-…

{"vehicle_id":"veh-1001","dealership_id":"d-001","vin":"1HGCM82633A004352","make":"Toyota","model":"Corolla","model_year":2023,"stocked_on":"2026-06-01","status":"IN_STOCK","sold_on":null,"age_days":150,"is_aging":true,"current_action":null,"version":1,"business_date":"2026-10-29"}
```

### Propose an action

This works only for an aging vehicle without an active action. `If-Match` is optional: send it
to make the command conditional on the version you saw.

```bash
curl -s -i -X POST $D/vehicles/veh-1001/actions -H "$ACTOR" -H "$JSON" -H 'If-Match: "1"' \
  -d '{"action_type": "PRICE_REDUCTION", "note": "Reduce by 5%"}'
# 201, Location: /v1/dealerships/d-001/vehicles/veh-1001/actions/<actionId>, ETag: "2"

ACTION=$(curl -s $D/vehicles/veh-1001 | jq -r .current_action.action_id)   # visible immediately
```

The `action_type` values are `PRICE_REDUCTION`, `PROMOTE`, `TRANSFER`, `AUCTION`, `WHOLESALE`,
`HOLD` and `OTHER`. `OTHER` requires a `note`.

### Revise, cancel, sell

```bash
curl -s -X PUT  $D/vehicles/veh-1001/actions/$ACTION -H "$ACTOR" -H "$JSON" \
  -d '{"action_type": "AUCTION", "note": "Lot 12"}' | jq '{version, current_action}'

curl -s -X POST $D/vehicles/veh-1001/actions/$ACTION/cancel -H "$ACTOR"      # the body is optional
curl -s -X POST $D/vehicles/veh-1001/actions/$ACTION/cancel -H "$ACTOR" -H "$JSON" -d '{"reason": "Buyer found"}'

curl -s -X POST $D/vehicles/veh-1001/sale -H "$ACTOR" -H "$JSON" -d '{"sold_on": "2026-10-29"}' | jq '{status, sold_on, version}'
```

## Queries (read model)

The read model is updated in the same transaction as the command's events, so a GET right
after a command returns the new `version`, and the detail `ETag` can be sent as `If-Match` at once.

```bash
# Oldest stock first (default), 20 per page
curl -s "$D/vehicles" | jq '{as_of, aging_threshold_days, page, ids: [.data[].vehicle_id]}'

# Aging stock only (IN_STOCK and older than 90 days)
curl -s "$D/vehicles?aging=true" | jq '.data[] | {vehicle_id, age_days, is_aging, current_action}'

# Filters: make/model are exact and case-insensitive; age bounds are inclusive
curl -s "$D/vehicles?make=toyota&model=corolla&min_age_days=60&max_age_days=180&sort=age_asc"

# Sold, or everything
curl -s "$D/vehicles?status=SOLD"
curl -s "$D/vehicles?status=ALL"

# Keyset pagination: pass next_cursor back unchanged (the filters must stay the same)
NEXT=$(curl -s "$D/vehicles?limit=2" | jq -r .page.next_cursor)
curl -s "$D/vehicles?limit=2&cursor=$NEXT" | jq '.data[].vehicle_id'

# What-if / boundary checks: compute ages at another business date
curl -s "$D/vehicles?aging=true&as_of=2026-12-01" | jq '.data | length'

# Detail (ETag = current version), action history (audit trail), dashboard summary
curl -s -i "$D/vehicles/veh-1001"
curl -s "$D/vehicles/veh-1001/actions" | jq '.data[] | {version, change, action_type, actor_id, correlation_id}'
curl -s "$D/inventory/summary" | jq
# {"as_of":"2026-10-29","aging_threshold_days":90,"in_stock":12,"aging":7,"aging_without_action":5,"oldest_stocked_on":"2026-05-02"}
```

## Errors (RFC 9457 problem+json)

Every error has a stable `code` and the `request_id` that appears in the server log.

```bash
curl -s -X POST $D/vehicles -H "$JSON" -d '{}' | jq .code                  # "ACTOR_REQUIRED"
curl -s -X POST $D/vehicles -H "$ACTOR" -H "$JSON" -d '{"vin":"BAD","make":"Kia","model":"Ceed","model_year":2024,"stocked_on":"2026-10-01"}' | jq '{code, errors}'
# "VALIDATION_ERROR", errors: [{"field":"vin","message":"must be 17 characters A-Z/0-9 excluding I, O and Q"}]
curl -s "$D/vehicles?agng=true" | jq .code                                  # "INVALID_QUERY" (typos are rejected)
curl -s "$D/vehicles/nope" | jq .code                                       # "VEHICLE_NOT_FOUND"
curl -s -X POST $D/vehicles/veh-1001/sale -H "$ACTOR" -H "$JSON" -d '{"sold_on":"2026-10-29"}' | jq .code   # "VEHICLE_ALREADY_SOLD"
curl -s -X POST $D/vehicles/veh-1001/actions -H "$ACTOR" -H "$JSON" -H 'If-Match: "1"' -d '{"action_type":"HOLD"}' | jq .code  # "VERSION_MISMATCH" (412)
```

| Status | Codes |
|---|---|
| 400 | `VALIDATION_ERROR`, `INVALID_BODY`, `INVALID_QUERY`, `INVALID_CURSOR`, `ACTOR_REQUIRED`, `INVALID_ACTOR`, `INVALID_IF_MATCH` |
| 404 | `DEALERSHIP_NOT_FOUND`, `VEHICLE_NOT_FOUND`, `NOT_FOUND` |
| 409 | `VEHICLE_ALREADY_EXISTS`, `VEHICLE_NOT_IN_STOCK`, `VEHICLE_ALREADY_SOLD`, `VEHICLE_NOT_AGING`, `ACTION_ALREADY_ACTIVE`, `ACTION_NOT_ACTIVE`, `CONCURRENCY_CONFLICT` |
| 412 | `VERSION_MISMATCH` |
| 413 / 415 | `BODY_TOO_LARGE` / `UNSUPPORTED_MEDIA_TYPE` |
| 500 | `INTERNAL` (no internal details are returned) |
| 503 | `UNAVAILABLE`, with `Retry-After: 1`. For a command the outcome is unknown, so check the vehicle before retrying. |

## Operations

```bash
curl -s $API/healthz; curl -s $API/readyz                    # process alive / writer and reader PostgreSQL pools reachable
curl -s $API/metrics | grep inventory_http_requests_total    # Prometheus metrics (inventory_http_*, Go, process)
curl -s -H 'X-Request-Id: my-trace-1' -o /dev/null -D - $D/vehicles | grep -i x-request-id   # correlation ID is kept
```

Rebuild the read model from the event store (recovery; commands wait until it commits):

```bash
docker compose --profile tools run --rm --no-deps rebuild      # containers
make rebuild-projections             # host (needs .env)
```
