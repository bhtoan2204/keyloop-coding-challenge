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

**Self-imposed (justify, don't assume):** ES/CQRS, MySQL event store → Debezium → Kafka → PostgreSQL read model.

## Current state (check with `git log`, `go test ./...`)
Implemented and tested:
- `package/eventsourcing`: `AggregateRoot` (versions, pending events, `ApplyChange`, `LoadFromHistory`), `Event`, serializer registry.
- `internal/infrastructure/eventstore`: `AggregateStore` (Get/Save in one tx: create-if-missing → CAS on
  `es_aggregate.version` → append `es_event` → optional snapshot), `repos` (GORM, raw SQL), versioned `Migrate`
  (v1 original DDL, v2 `es_event.aggregate_type`/`schema_version`). **Verified on SQLite and MySQL 8.4**
  (`docs/04` §8): concurrency, migrations/upgrade, error classes (`ErrConcurrencyConflict`, `ErrRetryable`,
  `IsRetryable`), restricted app user (`deploy/mysql/grants.sql`).
- `internal/domain`: `VehicleInventoryAggregate` (state, commands Stock/ProposeAction/ReviseAction/
  CancelAction/Sell, appliers, snapshot JSON), typed events, `Date`, `AgeInDays`, `IsAging`, domain errors.

- `internal/application/command`: command service (load → dealership ownership → If-Match → domain command at the
  dealership business date → `SaveWithMetadata`; reload-and-retry ≤ 3 on `IsRetryable`). `internal/presentation`:
  REST write API (5 commands, problem+json, `/healthz`, `/readyz`). `internal/infrastructure/referencedata`:
  dealership time zones (migration v3). `cmd/main.go api|migrate` (`make migrate`, `make run-api`).

- Read side (Phase 4): `internal/infrastructure/viewstore` (PostgreSQL 17 read model + migrations),
  `internal/infrastructure/projection` (`Projection.Apply`, `VehicleProjector`, `Runner` sync/rebuild from one
  consistent MySQL snapshot via `repos.EventStore.ReadAll`), `internal/application/query`, `presentation/*_handler.go`
  (4 GET endpoints). `cmd`: `sync-projections`, `rebuild-projections` (now bootstrap/recovery tools).
- Live change feed (Phase 5, `docs/05` §7): Compose `kafka` (KRaft) + `connect` (Debezium 3.6.2, connector
  `deploy/debezium/es-event-connector.json`, account `deploy/mysql/debezium-grants.sql`);
  `internal/application/messaging` (consumer: Debezium decoding, per-partition processing, offset commit after the
  PG commit, gap repair via `repos.EventStore.ReadStream`, startup sync, health); `internal/infrastructure/kafka`
  (franz-go transport); `projection.ConsumerAdapter`; `cmd projector` with `/healthz`, `/readyz`, `/status`.
  Pipeline tests: `internal/pipeline` (tags `integration pipeline`).

Not implemented yet: `Dockerfile` (Go processes run on the host), Idempotency-Key, OpenAPI, observability.
`.env` is git-ignored; copy `.env.example`.

## Reuse map (extend these; do not build parallel versions)
| Need | Use |
|---|---|
| Aggregate base, pending events, replay | `eventsourcing.AggregateRoot`, `ApplyChange(WithMetadata)`, `LoadFromHistory` |
| Event (de)serialisation | `eventsourcing.NewSerializer()` + `RegisterAggregate` |
| Load/save aggregates | `eventstore.AggregateStore` (`NewAggregateStoreWithSnapshotInterval`), `SaveWithMetadata` |
| Store port for commands | `command.VehicleStore`, implemented by `eventstore/commandstore` (maps store errors to `command.Err*`) |
| Transactions on the write side | `repos.EventStore.WithTransaction` |
| Projection | `projection.Projection` + `projectors.Projector` + `viewstore.ViewStore` (narrow refactor per `docs/05` §4.1) |
| Live consumer | `messaging.Consumer` with ports `Transport` (`kafka.Transport`), `Projector`/`Synchronizer` (`projection.ConsumerAdapter`), `EventHistory` (`repos.EventStore`) |
| Empty packages to fill | `internal/application/*`, `internal/presentation`, `cmd/main.go` |
| Leave unwired, do not delete | `projectors/projector_outbox.go`, `internal/infrastructure/cache`, `package/redis` |

## Source organization
Follow `.claude/rules/source-organization.md`: one responsibility per file, one business operation per file
when operations evolve independently, no catch-all `commands.go`/`queries.go`/`utils.go`, one field per line
in multiline struct literals, guard clauses, comments that explain *why*. Files near 250–300 lines and
functions near 40–60 lines are review triggers, not limits.

## Package boundaries
- `internal/domain/**`: no I/O, no SQL, no HTTP, no `time.Now()`. The business date is passed in by the caller.
- `internal/domain/events` may import `internal/domain` only. `aggregates` imports `domain`, `events`, `package/eventsourcing`.
- `internal/application` orchestrates (load → command → save, retries, business date). Infrastructure implements its interfaces.
- `internal/presentation` → `internal/application` only. No domain logic in handlers or SQL.
- Add an interface only where there is a second implementation (real DB + test fake). No generic frameworks.

## Event Sourcing / CQRS here
- Write: command method validates → `ApplyChange` → `Transition` → `applyX` mutates state (apply methods re-check
  ordering, so replay rejects bad history with `domain.ErrInvalidEventSequence`).
- Stored names are Go struct names (`VehicleInventoryAggregate`, `VehicleStockedEvent`, …) and are **frozen**.
- OCC: CAS on `es_aggregate.version` + unique `(aggregate_id, version)`. Conflicts are retried by re-loading
  and re-running the command, never by re-inserting.
- Read side: Debezium on `es_event` only, Kafka key = `aggregate_id`, one `Projection.Apply` tx per record (state,
  history and applied version together), duplicates skipped, gaps repaired from MySQL or halted, never skipped.
  Offsets are committed only after the PG commit. Rebuild/sync read MySQL, never Kafka.
- Read side is eventually consistent; aging is computed at query time (never stored).

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
make up && make test-integration          # real MySQL 8.4 + PostgreSQL 17; needs MYSQL_TEST_DSN and PG_TEST_DSN
make migrate && make mysql-grants && make pg-grants && make run-api   # local API
make cdc-up && make debezium-grants connector && make run-projector   # live change feed (docs/05 §7)
make sync-projections                     # point-in-time catch-up from MySQL (recovery; safe while projector runs)
make test-pipeline                        # real MySQL+Debezium+Kafka+PG; also needs KAFKA_TEST_BROKERS, CONNECT_TEST_URL
make check                                # fmt-check, vet (incl. integration + pipeline tags), test, test-race
```
Integration tests fail (never skip) without `MYSQL_TEST_DSN`. The dev machine uses rootless Podman
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
- Do not modify `package/eventsourcing` unless a failing test reproduces a bug. Then make the smallest fix and keep the public API.
- No `git reset --hard`, `git clean`, `git checkout -- .`, `git stash` or force-push. Do not edit `.git/` or real `.env*` files.
- No new dependencies, schema changes or Docker changes outside the phase that plans them.
- Never claim a test passed unless it was run in this session. Label unverified statements "unverified".

## Definition of done (every phase)
- `gofmt -l .` empty, `go vet ./...` clean, `go test ./...` and `go test -race ./...` pass (output quoted).
- New behaviour has tests that fail without the change (mutation-check important rules).
- Deviations from `docs/` are recorded in the relevant doc. `docs/ai-log.md` is updated.
- `git diff --stat` reviewed. Every deletion explained. No unrelated files touched.
