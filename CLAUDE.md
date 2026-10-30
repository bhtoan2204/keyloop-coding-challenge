# Keyloop Scenario B — Intelligent Inventory Dashboard (backend)

Take-home for Keyloop. Go backend with Event Sourcing + CQRS. **Deadline: Wednesday 4 November 2026.**
Spec: `KeyloopCodingChallange.pdf`. Design and plan: `docs/` (start at `docs/README.md`).

**Precedence:** current source code and the tests that actually run > `docs/` plans > assumptions.
If a doc disagrees with the code, trust the code. Report the mismatch and do not "fix" the code to match the doc.

## Keyloop-mandatory (from the PDF; never cut)
- B1/B2: filterable inventory list per dealership (make, model, age) with age in days.
- B3/B4: aging stock (in stock **> 90 calendar days**; exactly 90 is not aging) identified automatically
  and prominently: `is_aging` flag, `aging=true` filter, oldest-first, summary counts.
- B5/B6: a manager logs and persists a status/proposed action per aging vehicle, and its history is readable.
- Backend: REST API + persistent DB + OpenAPI/cURL. Deliverables: System Design Doc (with a GenAI section),
  README with build/run/test and an **AI Collaboration Narrative section**, core business-logic tests, 5–10 min video.

**Self-imposed (justify, don't assume):** ES/CQRS on one PostgreSQL 17 database: schema `event_store` (write model)
and schema `read_model` (read model), updated in the same transaction. Until Phase 7 the design was MySQL event store →
Debezium → Kafka → projector → PostgreSQL; that history stays in `docs/02`, `04`, `05`, `07` (marked superseded).

## Current state (check with `git log`, `go test ./...`)
Implemented and tested (runtime: PostgreSQL 17 + the `api` process + one-shot init jobs `migrate`, `postgres-grants`):
- `pkg/eventsourcing`: `AggregateRoot` (versions, pending events, `ApplyChange`, `LoadFromHistory`), `Event`, serializer registry.
- `internal/domain`: `VehicleInventoryAggregate` (state, commands Stock/ProposeAction/ReviseAction/
  CancelAction/Sell, appliers, snapshot JSON), typed events, `Date`, `AgeInDays`, `IsAging`, domain errors.
- `internal/infrastructure/eventstore`: `AggregateStore`. One transaction per save: projection lock (shared
  advisory lock 52003) → create-if-missing → CAS on `event_store.es_aggregate.version` → `INSERT es_event … RETURNING id`
  (ids written back into the events) → `TransactionalProjection.Apply` on the same `*gorm.DB` tx (`WithProjection`) →
  optional snapshot → commit; `root.Update()` only after commit; nested save refused (`ErrNestedSave`). `repos`: GORM, raw
  schema-qualified SQL, versioned `Migrate` (event_store v1 tables, v2 `dealership`, advisory lock 51001), error classes
  (`ErrConcurrencyConflict`, `ErrRetryable` for 40001/40P01/55P03, `IsUnavailable` for 57014/57P0x/08xxx/network).
- `internal/application/command`: command service (load → dealership ownership → If-Match → domain command at the
  dealership business date → `SaveWithMetadata`; reload-and-retry ≤ 3 on retryable errors, never on unavailable).
  `internal/infrastructure/referencedata`: dealership time zones from `event_store.dealership`.
- Read side (Phase 7.1 layout):
  - `internal/application/projection`: the projection write port `Store` and its models
    (`ProjectedVehicle`, `ActionHistoryEntry`). `internal/application/query`: `ReadModel` port.
  - `internal/infrastructure/viewstore`: `Postgres` (implements `projection.Store`, transaction-bound;
    `RunExclusive` for rebuilds) and `ReadModel` (implements `query.ReadModel`), both composing
    `viewstore/repositories` (Vehicle, ActionHistory, InventorySummary, Projection, Dealership; list query from
    named GORM scopes in `vehicle_filters.go`; mappers in `mappers.go`) over `viewstore/models` (GORM row types,
    `models.Date`). `viewstore/migrations`: `read_model` v1 for a fresh database only; an earlier `public`
    layout is refused, never adopted.
  - `internal/infrastructure/projection` (`Projection.Apply`, `VehicleProjector`, `CommandProjection` for the save
    transaction, `Runner.Rebuild`), `presentation/queries` (4 GET endpoints). The read model is updated atomically
    with the events: read-after-write is immediate.
- `cmd`: `api`, `migrate`, `rebuild-projections` (one transaction, exclusive projection lock, rebuild role), `healthcheck URL`.
- Roles (`deploy/postgres`): admin (`POSTGRES_USER`, migrate/grants only), writer `inventory_app` (`app-grants.sql`,
  append-only `es_event`), reader `inventory_reader` (`grants.sql`), rebuild `inventory_rebuild` (`rebuild-grants.sql`).
- Packaging and contract: multi-stage `Dockerfile` (distroless, nonroot), Compose stack with one-shot init services
  (`make stack-up`), `api/openapi.yaml` (embedded; `/openapi.yaml`, `/docs`; `openapi_contract_test.go`), Prometheus
  `/metrics` (`inventory_http_*`), `internal/e2e` (`make e2e`, asserts immediate read-after-write).
  Submission docs: `README.md` (AI Collaboration Narrative), `docs/system-design.md`, `docs/demo.md`, `docs/curl-examples.md`.

Removed in Phase 7: MySQL, Debezium, Kafka, Kafka Connect, the `projector` process, `sync-projections`, `/status`,
`internal/application/messaging`, `internal/infrastructure/kafka`, `internal/pipeline`. Removed in Phase 7.1: the one-off
MySQL history import and the in-place upgrade of the Phase 4–6 `public` read model (fresh installations only).
Not implemented: Idempotency-Key, authentication, VIN uniqueness (D4), OpenTelemetry export.
`.env` is git-ignored; copy `.env.example`.

## Reuse map (extend these; do not build parallel versions)
| Need | Use |
|---|---|
| Aggregate base, pending events, replay | `eventsourcing.AggregateRoot`, `ApplyChange(WithMetadata)`, `LoadFromHistory` |
| Event (de)serialisation | `eventsourcing.NewSerializer()` + `RegisterAggregate` |
| Load/save aggregates | `eventstore.AggregateStore` (`NewAggregateStoreWithSnapshotInterval`), `SaveWithMetadata` |
| Store port for commands | `command.VehicleStore`, implemented by `eventstore/commandstore` (maps store errors to `command.Err*`) |
| Transactions on the write side | `repos.EventStore.WithTransaction` |
| Projection | `projection.Projection` + `projectors.Projector` over the application port `application/projection.Store` |
| Read-model persistence | `viewstore/repositories` (one repository per table group, built on the caller's `*gorm.DB`), `viewstore/models`, mappers in `repositories/mappers.go`; compose them in `viewstore.Postgres` / `viewstore.ReadModel` |
| List filters | named scopes in `repositories/vehicle_filters.go` (add a scope, do not concatenate SQL) |
| Read model in the command transaction | `eventstore.TransactionalProjection` via `eventstore.WithProjection`, implemented by `projection.CommandProjection` |
| Rebuild | `projection.Runner.Rebuild` over `viewstore.Postgres.RunExclusive` and `repos.EventStore.ReadAll` |

## Source organization
Follow `.claude/rules/source-organization.md`:
- Files: one responsibility per file, one business operation per file when operations evolve independently,
  no catch-all `commands.go`/`queries.go`/`utils.go`. Files near 250–300 lines and functions near 40–60 lines
  are review triggers, not limits.
- Packages: group related files into cohesive packages (no flat directories of unrelated features, no package
  per file). Children never import the parent; shared helpers never import features; no sideways imports.
  Small exported APIs; keep public entry points stable via aliases/facades.
- Comments only where the code cannot speak (invariants, transactions, concurrency, replay/schema,
  idempotency, security, trade-offs, workarounds, non-obvious contracts); drop narration.
- Readability: one field per line in multiline struct literals, readable long calls, guard clauses;
  `gofmt` + `goimports -local keyloop-coding-challenge`.

## Package boundaries
- `internal/domain/**`: no I/O, no SQL, no HTTP, no `time.Now()`. The business date is passed in by the caller.
- `internal/domain/events` may import `internal/domain` only. `aggregates` imports `domain`, `events`, `pkg/eventsourcing`.
- `internal/application` orchestrates (load → command → save, retries, business date). Infrastructure implements its interfaces.
- `internal/presentation` → `internal/application` only. No domain logic in handlers or SQL. Layout: root
  `router.go` composes `commands` (write endpoints), `queries` (read endpoints) and `probes` (healthz/readyz/
  metrics) over the shared transport `httpkit`; `application_ports.go` keeps the public type names as aliases.
- Repository and port interfaces belong to their consumer: `command.VehicleStore`, `query.ReadModel`,
  `projection.Store` live in `internal/application`; infrastructure implements them (pinned by
  `internal/application/layer_dependencies_test.go`). Persistence models (`viewstore/models`) and SQL stay in
  infrastructure; application models never import GORM. `viewstore` children never import the root
  (`viewstore/package_dependencies_test.go`).
- Every repository of one command write is built on the SAME transaction handle; repositories never begin or
  commit a transaction and never fall back to the root pool.
- Add an interface only where there is a second implementation (real DB + test fake) or a consumer that owns
  the contract. No generic frameworks, no generic CRUD `Repository[T]`.

## Event Sourcing / CQRS here
- Write: command method validates → `ApplyChange` → `Transition` → `applyX` mutates state (apply methods re-check
  ordering, so replay rejects bad history with `domain.ErrInvalidEventSequence`).
- Stored names are Go struct names (`VehicleInventoryAggregate`, `VehicleStockedEvent`, …) and are **frozen**.
- OCC: CAS on `es_aggregate.version` + unique `(aggregate_id, version)`. Conflicts are retried by re-loading
  and re-running the command, never by re-inserting.
- Read side: the command's save transaction applies its events through the same `Projection.Apply` a rebuild uses
  (state, history and applied version with the events, no savepoints). A version already projected fails the save
  (`ErrVersionGap`: read model ahead), a gap fails it too; nothing is skipped. Lock order: projection advisory lock
  (shared for commands, exclusive for a rebuild) before any event-store row lock.
- Rebuild reads `event_store` in the same transaction that truncates and rewrites `read_model`; a failure changes nothing.
- The read model is consistent with the event store at every commit; aging is computed at query time (never stored).

## Business invariants (`internal/domain/aggregates/vehicle_inventory_*.go`)
Stock once per aggregate · actions need IN_STOCK · propose needs aging (> 90 days at business date) ·
at most one active action · revise/cancel need the matching active `action_id` · SOLD is terminal and a
sale closes the active action · VIN 17 chars without I/O/Q · `stocked_on ≤ business date` ·
identical revision records no event · versions contiguous from 1.

## Commands
```bash
gofmt -l .                     # must print nothing
go vet ./...
go test -count=1 ./...         # unit + SQLite-backed store tests (needs cgo for go-sqlite3)
go test -race -count=1 ./...
make up && make test-integration          # real PostgreSQL 17; needs PG_TEST_DSN (admin URL, no database)
make up && make migrate && make pg-grants && make run-api   # local API (pg-grants: writer, reader, rebuild roles)
make rebuild-projections                  # recovery: rebuild read_model from event_store in one transaction
make check                                # fmt-check, vet (incl. integration, e2e tags), test, test-race
make stack-up && make e2e                 # whole system in containers + business flow against it
```
Integration tests fail (never skip) without `PG_TEST_DSN`; each creates and drops its own database and roles. The dev machine uses rootless Podman
(`docker compose` → podman-compose); Testcontainers is not used.

## Workflow (one phase at a time, see `docs/07-implementation-plan.md`)
1. `/keyloop-plan` → inspect `git status` and code, write the minimal-change plan, wait for approval.
2. Implement in small, localized diffs in existing packages. `gofmt` and tests after each step.
3. `/keyloop-test` → run the commands above and report real output. Add tests for gaps found.
4. `/keyloop-review` (and `/keyloop-security` when touching SQL, HTTP, config or containers).
5. Append a factual entry to `docs/ai-log.md` (what was delegated, verified, corrected).
6. Stop. The developer reviews and commits. **Never commit or push without explicit approval.**

## Restrictions
- Never delete or rewrite whole packages to adopt a different design. Behaviour changes need a stated reason
  and a test that shows them. Behaviour-preserving moves between files of a package are fine (and expected
  when a file mixes responsibilities); a file may be removed once all its declarations have moved.
- Never weaken an assertion just to make a test pass. Adapt a test only when it contradicts a valid invariant, and say so.
- Do not modify `pkg/eventsourcing` unless a failing test reproduces a bug. Then make the smallest fix and keep the public API.
- No `git reset --hard`, `git clean`, `git checkout -- .`, `git stash` or force-push. Do not edit `.git/` or real `.env*` files.
- No new dependencies, schema changes or Docker changes outside the phase that plans them.
- Never claim a test passed unless it was run in this session. Label unverified statements "unverified".

## Definition of done (every phase)
- `gofmt -l .` empty, `go vet ./...` clean, `go test ./...` and `go test -race ./...` pass (output quoted).
- New behaviour has tests that fail without the change (mutation-check important rules).
- Deviations from `docs/` are recorded in the relevant doc. `docs/ai-log.md` is updated.
- `git diff --stat` reviewed. Every deletion explained. No unrelated files touched.
