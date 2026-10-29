# Demo script and video outline (5–10 minutes)

This is an **outline** for the walkthrough video. **No video has been recorded yet.** Timings are targets. The commands assume the stack started with `make stack-up`, plus `jq`.

Preparation:

- **Data:** start from a fresh stack (`docker compose down -v`, then `make stack-up`) so IDs and counts are predictable.
- **Panes:** keep three terminals visible: requests, `make stack-logs`, and `watch -n1 'curl -s 127.0.0.1:8081/status | jq'`.
- **Editor tabs:** `docs/system-design.md` (diagram) and the files listed in section 3.

## 1. Problem and requirements (0:00–0:45)

- **Scenario B:** a dealership manager must see stock with its age, spot vehicles **in stock for more than 90 days**, and record what will be done about each one (e.g. "Price Reduction Planned"), with a history.
- **Mandatory:** REST API, persistent database, OpenAPI/cURL, tests, design document, AI narrative.
- **Self-imposed, and why:** Event Sourcing gives the audit trail and safe concurrent actions; CQRS + CDC gives a rebuildable read model.
- Show the requirements table in the README.

## 2. Architecture (0:45–2:00)

Walk through the diagram in `docs/system-design.md` §2.

- **Write side.** A command loads the vehicle's events from MySQL, applies the rule, and appends events with a version CAS, all in one transaction.
- **Change feed.** Debezium reads the binlog and publishes to Kafka, keyed by vehicle, so the events of one vehicle stay in order.
- **Projector.** Applies each event in one PostgreSQL transaction, with a version check that makes it idempotent. It commits the Kafka offset only after that.
- **Read side.** PostgreSQL serves the queries, and aging is computed per request.
- **Consistency:** commands are strongly consistent; queries are eventually consistent, at about 0.5 s locally.

## 3. Important source files (2:00–3:15)

| File | Point to make |
|---|---|
| `internal/domain/aggregates/vehicle_inventory_propose_action.go` | Invariants (in stock, aging at the business date, one active action), then `ApplyChange`; the applier re-checks on replay |
| `internal/domain/inventory.go` | Aging is a pure function of `stocked_on` and a date; it is never stored |
| `internal/infrastructure/eventstore/aggregate_store.go` | One transaction: CAS on `es_aggregate.version`, append, optional snapshot |
| `internal/application/command/execution.go` | Reload-and-retry on conflict (3 attempts), `If-Match` check |
| `internal/infrastructure/projection/projection.go` | Per-vehicle lock, duplicate skip, gap error, state + history + version in one transaction |
| `internal/application/messaging/offsets.go`, `gap_repair.go` | Offset after the PostgreSQL commit; failed records are rewound, never skipped; gaps repaired from MySQL |
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
- **What the tests show:** concurrent writers on one vehicle give exactly one winner, and the losers are reloaded and re-run. A real MySQL deadlock (error 1213) was found by a test and fixed (`docs/system-design.md` §9).
- **The history in MySQL:**
  ```bash
  docker compose exec mysql sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e "SELECT id, aggregate_id, event_type, version FROM inventory.es_event WHERE aggregate_id=\"demo-120\""'
  ```

## 6. Live Kafka projection (6:30–7:45)

- In the `/status` pane, `events_applied` grows after each command.
- **Stop the projector, write, start it again:**
  ```bash
  docker compose stop projector
  curl -s -X POST $D/vehicles/demo-91/actions -H "$A" -H "$J" -d '{"action_type":"HOLD"}' > /dev/null
  curl -s $D/vehicles/demo-91 | jq '{version, current_action}'      # still the old version: the read model is behind
  docker compose up -d --no-deps projector
  curl -s $D/vehicles/demo-91 | jq '{version, current_action}'      # caught up; the history is not duplicated
  ```
- **Rebuild from MySQL while it runs:**
  ```bash
  docker compose run --rm --no-deps projector rebuild-projections
  ```
- **Metrics:**
  ```bash
  curl -s 127.0.0.1:8081/metrics | grep inventory_projection_
  ```

## 7. Testing evidence (7:45–8:30)

- **Commands to show:** `make check`, `make test-integration`, `make test-pipeline` and `make e2e`. Show the output recorded in `docs/ai-log.md` (Phase 6) rather than running the slow suites live.
- **Highlights:**
  - aging boundary tests (89/90/91, DST, leap year);
  - the pipeline tests on real Kafka/Debezium (crash before and after commit, connector restart, bootstrap while writing, truncate detection);
  - mutation checks;
  - the OpenAPI-versus-routes contract test.

## 8. AI collaboration (8:30–9:30)

Summarise the README's narrative, using `docs/ai-log.md` for specifics:

- **Rules, skills and phase gates:** project rules, skills, reviewer agents and a hook guided the AI. Each phase was planned, approved, tested, reviewed and logged.
- **A rejected direction:** the AI proposed rewriting the framework without running its tests, and the developer rejected it.
- **Real defects found by verification:** independent review and mutation tests found the deadlock, the 409/412 bug, the Kafka retention skip and the dropped truncates.
- **Honesty about mistakes:** they are listed, not hidden.

## 9. Trade-offs and next steps (9:30–10:00)

- **Trade-offs:** two databases and a CDC pipeline are heavy for this domain size. They were chosen for audit, safe concurrency and rebuildability, and are self-imposed.
- **Next steps:**
  - authentication and per-dealership authorisation;
  - `Idempotency-Key`;
  - VIN uniqueness;
  - a dependency refresh (pgx, the Go toolchain);
  - a read-only MySQL user for the projector;
  - tracing export and alerts;
  - load testing.
