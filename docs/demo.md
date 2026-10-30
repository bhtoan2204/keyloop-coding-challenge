# Demo script and video outline (5–10 minutes)

This is an **outline** for the walkthrough video. **No video has been recorded yet.** Timings are targets. The commands assume the stack started with `make stack-up`, plus `jq`.

Preparation:

- **Data:** start from a fresh stack (`docker compose down -v`, then `make stack-up`) so IDs and counts are predictable.
- **Panes:** keep three terminals visible: requests, `make stack-logs`, and a `psql` session (`docker compose exec postgres psql -U inventory -d inventory_read`).
- **Editor tabs:** `docs/system-design.md` (diagram) and the files listed in section 3.

## 1. Problem and requirements (0:00–0:45)

- **Scenario B:** a dealership manager must see stock with its age, spot vehicles **in stock for more than 90 days**, and record what will be done about each one (e.g. "Price Reduction Planned"), with a history.
- **Mandatory:** REST API, persistent database, OpenAPI/cURL, tests, design document, AI narrative.
- **Self-imposed, and why:** Event Sourcing gives the audit trail and safe concurrent actions; CQRS gives a query-shaped, rebuildable read model. Both live in one PostgreSQL database.
- Show the requirements table in the README.

## 2. Architecture (0:45–2:00)

Walk through the diagram in `docs/system-design.md` §2.

- **Write side.** A command loads the vehicle's events from `event_store`, applies the rule, and appends events with a version CAS.
- **Read side in the same transaction.** The same transaction applies the new events to `read_model` through the projection a rebuild uses, with a per-vehicle version check. Events and read model commit together or not at all.
- **Queries** read `read_model` with a read-only role; aging is computed per request.
- **Consistency:** a GET right after a command returns the new version.
- **History:** until Phase 7 this was MySQL → Debezium → Kafka → projector → PostgreSQL. Say why it was dropped (§9 below).

## 3. Important source files (2:00–3:15)

| File | Point to make |
|---|---|
| `internal/domain/aggregates/vehicle_inventory_propose_action.go` | Invariants (in stock, aging at the business date, one active action), then `ApplyChange`; the applier re-checks on replay |
| `internal/domain/inventory.go` | Aging is a pure function of `stocked_on` and a date; it is never stored |
| `internal/infrastructure/eventstore/aggregate_store.go` | One transaction: projection lock, CAS on `es_aggregate.version`, append with `RETURNING id`, projection, optional snapshot; pending events acknowledged only after commit |
| `internal/application/command/execution.go` | Reload-and-retry on conflict (3 attempts), `If-Match` check |
| `internal/infrastructure/projection/projection.go`, `command_projection.go` | Per-vehicle lock and version check; in a command, a gap or a read model that is ahead fails the whole save |
| `internal/infrastructure/projection/runner.go` | Rebuild: exclusive lock, truncate and replay in one transaction |
| `internal/presentation/` | `commands`, `queries`, `probes` and `apidocs` over a shared `httpkit`; `api/openapi.yaml` is served at `/docs` |

## 4. Core business flow, live (3:15–5:30)

```bash
API=http://127.0.0.1:8080; D=$API/v1/dealerships/d-001
A='X-Actor-Id: manager-42'; J='Content-Type: application/json'

# Stock three vehicles: 120, 91 and 90 days ago
for age in 120 91 90; do
  curl -s -X POST $D/vehicles -H "$A" -H "$J" -d "{\"vehicle_id\":\"demo-$age\",\"vin\":\"1HGCM82633A004352\",\"make\":\"Toyota\",\"model\":\"Corolla\",\"model_year\":2023,\"stocked_on\":\"$(date -d "-$age days" +%F)\"}" | jq -c '{vehicle_id, age_days, is_aging, version}'
done

curl -s "$D/vehicles?aging=true" | jq '[.data[] | {vehicle_id, age_days, is_aging}]'   # 120 and 91 only: exactly 90 is not aging
curl -s "$D/inventory/summary" | jq                                                    # dashboard tiles
curl -s "$D/vehicles/demo-90?as_of=$(date -d '+1 day' +%F)" | jq '{age_days, is_aging}'  # tomorrow it turns

# A manager logs a plan for the oldest one, revises it, and the car is sold
curl -s -i -X POST $D/vehicles/demo-120/actions -H "$A" -H "$J" -H 'If-Match: "1"' -d '{"action_type":"PRICE_REDUCTION","note":"Reduce by 5%"}' | grep -E 'HTTP|Location|Etag'
ACT=$(curl -s $D/vehicles/demo-120 | jq -r .current_action.action_id)
curl -s -X PUT $D/vehicles/demo-120/actions/$ACT -H "$A" -H "$J" -d '{"action_type":"AUCTION","note":"Lot 12"}' | jq -c '{version, current_action}'
curl -s -X POST $D/vehicles/demo-90/actions -H "$A" -H "$J" -d '{"action_type":"HOLD"}' | jq -c '{status, code, detail}'   # 409 VEHICLE_NOT_AGING
curl -s -X POST $D/vehicles/demo-120/sale -H "$A" -H "$J" -d "{\"sold_on\":\"$(date +%F)\"}" | jq -c '{status, current_action, version}'
curl -s $D/vehicles/demo-120/actions | jq '[.data[] | {version, change, action_type, actor_id}]'   # PROPOSED → REVISED → CLOSED_BY_SALE
```

## 5. Event Sourcing and concurrency (5:30–6:30)

- A stale `If-Match` gives `412 VERSION_MISMATCH`, and nothing is changed:
  ```bash
  curl -s -X POST $D/vehicles/demo-91/actions -H "$A" -H "$J" -H 'If-Match: "7"' -d '{"action_type":"HOLD"}' | jq -c '{status, code}'
  ```
- **What the tests show:** concurrent writers on one vehicle give exactly one winner, and the losers are reloaded and re-run (`docs/system-design.md` §9). A failed transaction leaves no event and no read-model change.
- **The history and the read model side by side** (in the `psql` pane):
  ```sql
  SELECT id, event_type, version FROM event_store.es_event WHERE aggregate_id = 'demo-120' ORDER BY version;
  SELECT version, last_event_id FROM read_model.projection_aggregate WHERE aggregate_id = 'demo-120';
  ```

## 6. Read-after-write and rebuild (6:30–7:45)

- **Immediate read-after-write:** use the ETag of a GET as `If-Match` straight away:
  ```bash
  V=$(curl -s $D/vehicles/demo-91 | jq -r .version)
  curl -s -X POST $D/vehicles/demo-91/actions -H "$A" -H "$J" -H "If-Match: \"$V\"" -d '{"action_type":"HOLD"}' | jq -c '{version, current_action}'
  curl -s $D/vehicles/demo-91 | jq -c '{version, current_action}'      # same version, no waiting
  ```
- **Rebuild from the event store** (recovery; commands wait while it runs):
  ```bash
  docker compose --profile tools run --rm --no-deps rebuild
  curl -s $D/vehicles/demo-91/actions | jq '.data | length'            # same history as before
  ```
- **Metrics:**
  ```bash
  curl -s $API/metrics | grep inventory_http_problems_total
  ```

## 7. Testing evidence (7:45–8:30)

- **Commands to show:** `make check`, `make test-integration` and `make e2e`. Show the output recorded in `docs/ai-log.md` rather than running the slow suites live.
- **Highlights:**
  - aging boundary tests (89/90/91, DST, leap year);
  - the PostgreSQL tests: a failed projection or lock timeout rolls back events and read model together; immediate read-after-write; rebuild equal to the live read model, also with concurrent commands;
  - mutation checks;
  - the OpenAPI-versus-routes contract test.

## 8. AI collaboration (8:30–9:30)

Summarise the README's narrative, using `docs/ai-log.md` for specifics:

- **Rules, skills and phase gates:** project rules, skills, reviewer agents and a hook guided the AI. Each phase was planned, approved, tested, reviewed and logged.
- **A rejected direction:** the AI proposed rewriting the framework without running its tests, and the developer rejected it.
- **Real defects found by verification:** independent review and mutation tests found the deadlock, the 409/412 bug, the Kafka retention skip and the dropped truncates (all but the 409/412 bug belong to the earlier MySQL/Kafka design).
- **Honesty about mistakes:** they are listed, not hidden.

## 9. Trade-offs and next steps (9:30–10:00)

- **Trade-offs:** Event Sourcing is self-imposed, for audit, safe concurrency and rebuildability. In Phase 7 the developer decided to drop the MySQL/Debezium/Kafka pipeline: its operational cost outweighed its benefit at this scale, and immediate read-after-write matters for the manager UI. The price: the projection runs on each command's critical path, a rebuild blocks commands, and everything is on one PostgreSQL primary.
- **Next steps:**
  - authentication and per-dealership authorisation;
  - `Idempotency-Key`;
  - VIN uniqueness;
  - an online (shadow-table) rebuild;
  - tracing export and alerts;
  - load testing.
