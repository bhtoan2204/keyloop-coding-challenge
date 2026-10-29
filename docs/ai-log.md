# AI Collaboration Log

Factual, append-only record of what the AI assistant (Claude Code, model Claude Opus 5.5) produced, what the developer accepted, rejected or corrected, and how claims were verified. This is the only source for the README's AI Collaboration Narrative and the SDD's GenAI-in-design section (D11). Entries record only things that actually happened. "Verified" means a command was run and its output observed.

## 2026-10-29 — Planning (docs 01–08)

- **Delegated:** analyse Scenario B, list assumptions, review the proposed architecture, and design the domain, event store, read model, API and plan as Markdown under `docs/`.
- **AI output:** `docs/README.md` and `01`–`08`.
- **Correction 1, missed requirement source:** the AI looked for the challenge PDF with `git ls-files`, which lists only tracked files. The PDF was untracked, so it was missed. The AI's next attempt, a filesystem-wide `find /`, was rejected by the developer. The first draft was therefore written from the developer's restated brief, not the PDF. The developer gave the file path. The AI read the PDF and revised the docs. That revision added the summary endpoint (D10), the "real-time" latency target (B7), the AI Collaboration Narrative as a README section, and the split between Keyloop-mandatory and self-imposed requirements.
- The developer committed the docs as `dde142b`.

## 2026-10-29 — Rejected direction: rewrite of the existing framework

- **AI output (in the `dde142b` docs):** recommendations to replace GORM/SQLite with `database/sql`/`pgx`, drop snapshots, drop the `es_aggregate` CAS row, replace the reflection-based serializer and event naming, and move code into a new `internal/inventory` layout (old D1, D2).
- **Problem:** several of these claims were made **without running the existing tests or probing the code**. Examples: "snapshots are fragile" and "the unique key alone suffices".
- **What happened next:** asked to implement "Phase 1", the AI ran `git rm` on `package/`, `internal/` and `cmd/main.go`. It rewrote `go.mod` (removing the GORM/SQLite dependencies) and began a new `internal/inventory/domain` package with its own tests. Those tests passed, and a mutation check (aging `>` changed to `>=`) made 3 tests fail as intended. The developer interrupted the phase.
- **Developer decision:** the rewrite direction was **rejected**. The new direction is to *preserve and incrementally improve the existing source code*. When Phase 0 began, the working tree was clean at `dde142b`, with the original framework present and none of the interrupted work. **No result from the interrupted work applies to the current codebase.**

## 2026-10-29 — Phase 0: documentation reconciliation

- **Delegated:** inspect the existing code and tests, reconcile the docs with it, documentation changes only.
- **Verified baseline** (Go 1.26.5, working tree at `dde142b`):
  - `go vet ./...`: no findings
  - `go test -count=1 ./...`: 17 tests pass in 3 packages (`package/eventsourcing` 6, `internal/domain/aggregates` 1, `internal/infrastructure/eventstore` 10)
  - `go test -race -count=1 ./...`: pass
  - All event-store tests run on SQLite. **No test touches MySQL.**
- **Probe (verified, scratch copy outside the repo, not committed):** a test aggregate with state in unexported fields and no custom JSON, saved with snapshot interval 2 and reloaded. The snapshot row contained `{}`. The reloaded aggregate had version 3 but empty state. This confirms a state-loss hazard for aggregates shaped like `VehicleInventoryAggregate` (04 §7). The decision is to keep snapshots and disable them for vehicles (D2 revised).
- **Not verified, recorded as hypothesis H1:** a possible MySQL deadlock between `INSERT IGNORE` and the CAS `UPDATE` under concurrent saves. To be tested in Phase 1 before any fix (04 §5).
- **AI output:** revised `README.md`, `02`, `03`, `04`, `05`, `07`, consistency edits in `01`, `06`, `08`, and this log. No Go source, schema, dependency or Docker file was changed.

## 2026-10-29 — Phase 1: Vehicle Inventory domain on the existing framework

- **Delegated:** state, commands, typed event payloads, aging rule, domain errors and tests on the existing `VehicleInventoryAggregate`, `AggregateRoot.ApplyChange`, `Transition` and serializer registration. No framework, repository, Kafka, Postgres, Compose or HTTP changes.
- **AI output:** new `internal/domain/{date,inventory}.go`, extended `errors.go`, payload fields on the 5 existing event structs, state/commands/appliers in `aggregates/vehicle_inventory.go`, `aggregates/commands.go`, snapshot JSON in `aggregates/vehicle_inventory_snapshot.go`, tests. `package/eventsourcing` was not modified: no framework bug was found that blocked the domain.
- **Legacy test adapted, not weakened:** `TestVehicleEventsUsePointerPayloads` applied empty payloads in the order Stocked, Sold, Proposed, …, which the new invariants correctly reject (empty `VehicleStockedEvent`; actions after a sale). Its checks were kept: duplicate registration rejected, every pinned event name registered and serialisable, value payload rejected, pending versions 1..5 with the pinned names. The names are now produced by a valid command sequence.
- **Self-correction:** a revise subtest was named "not aging at revision time" but used a 91-day vehicle, so it tested nothing new. It was removed instead of being kept with a misleading name.
- **Verified:**
  - `go vet ./...` clean
  - `go test -count=1 ./...` and `go test -race -count=1 ./...`: all packages pass, with 38 top-level tests and 100 subtests passing (`-v` count)
  - **Mutation checks** on a scratch copy, each expected to fail: aging `>`→`>=` (TestIsAging, TestProposeAction failed); dropped sale/active-action consistency check (replay tests failed); snapshot dropping `soldOn` (TestSnapshotJSONRoundTrip failed); propose ignoring an active action (TestProposeAction, TestSnapshotThroughAggregateStore failed); stocking twice allowed (TestStock, TestCommandsAfterSale, replay tests failed)
- **Process note:** the AI ran `git add -N` to show new files in `git diff --stat`, then undid it with `git reset -q -- internal/domain`. The index was left as found, and nothing was committed.

## 2026-10-29 — Phase 2: MySQL event store (existing framework extended)

- **Delegated:** MySQL 8.4 support for the existing GORM event store, versioned additive migrations, error classification, CDC-ready columns, real-MySQL integration tests, a local Compose setup, restricted grants. The developer approved the plan (Q1–Q7) before implementation.
- **Environment:** MySQL 8.4.13 in podman-compose (`docker compose` is the podman wrapper here), bound to 127.0.0.1. The root password was random, kept in the session scratchpad and passed through the environment. No credentials were written to the repo, and the hook blocks edits to `.env`. `.env` (empty) was untracked with `git rm --cached .env` after checking it was 0 bytes; the local file remains.
- **Baseline before changes:** the 10 existing `AggregateStore` tests were pointed at MySQL through a small test-helper change. All passed unmodified.
- **Hypothesis H1 tested before touching code. Confirmed.** The new concurrency tests showed `Error 1213 Deadlock` (6/16 writers from the same version, 1/16 on concurrent creation). InnoDB's deadlock report showed both transactions holding S record locks on the `es_aggregate` row and waiting for X. Fix: `INSERT IGNORE` → `INSERT … ON DUPLICATE KEY UPDATE id = id`, a one-statement change. After the fix, 3 runs × 10 rounds × 16 writers gave exactly 1 winner, the rest `ErrConcurrencyConflict`, and 0 other errors.
- **Error classification:** typed `*mysql.MySQLError` (1062 → duplicate; 1213/1205 → new `ErrRetryable`), plus `eventstore.IsRetryable`. No retry loop in the store. 1205 was verified with a real held row lock and `innodb_lock_wait_timeout=1`.
- **Migrations:** `repos.Migrate` became a small versioned migrator. v1 is the original DDL unchanged; v2 adds `es_event.aggregate_type` (backfilled) and `schema_version`. Each step is idempotent, and concurrent callers are serialised with a MySQL `GET_LOCK`. Tests cover a fresh database, an upgrade from a frozen copy of the original schema with rows written the original way, resuming after a partial run, and 4 concurrent migrators. All run on SQLite and MySQL.
- **Framework change (approved Q1):** `eventsourcing.Event` gained `AggregateType` and `SchemaVersion` (additive), plus `DefaultSchemaVersion`. No existing API changed.
- **AI mistakes caught and corrected during the phase:**
  - (1) A test file was first written referencing helpers that did not exist (`newStoreFor`, `qtyStore`, a wrong register-func type); `go vet` caught it before any run.
  - (2) The first mutation harness compared each file with itself (`git -C <repo> diff --no-index` resolves relative paths against the repo), so it reported every mutation as "not applied". Fixed with `cmp`.
  - (3) Two mutants were invalid: a `sed` delimiter clash, and a "missing return" build failure. They had looked like survivors because the filter only matched `--- FAIL`. They were redone as compiling mutants with build status shown, and both were then caught.
- **Developer edit observed:** the explanatory comment the AI wrote above the new `ON DUPLICATE KEY UPDATE` statement was removed in the working tree by someone other than the AI. It was not re-added; the rationale lives in `docs/04` §5.
- **Verified:** `make check` (gofmt, vet incl. `-tags integration`, test, race) passes. `make test-integration` passes 24 + 2 tests on MySQL 8.4.13 (28 + 2 after the review fixes below). The integration suite also passes under `-race`. Mutation checks caught all 6 valid mutants. 0 leftover test databases or users after the runs.
- **Not verified:** `make mysql-grants` end to end (the same `grants.sql` is applied by the integration test), MySQL behaviour under real network failures, and any Debezium behaviour (not in scope).
- **Independent review** (read-only `event-sourcing-reviewer` subagent; it could read code and run SQLite tests but not MySQL): no Critical findings.
  - Its Major 2 ("MySQL behaviour unverified") was outdated: the MySQL suite had already been run, see above.
  - **Accepted and fixed, each with a test that a mutation check confirmed would fail:**
    - `schema_version` was read but never checked (Major 1) → `ErrUnsupportedSchemaVersion`;
    - `Append` accepted an empty `aggregate_type`, and the event-level type check was untested → type now required, mismatch tested;
    - the migration lock name could exceed 64 characters → `MD5(DATABASE())`;
    - the migrator accepted a schema newer than the code → now fails;
    - the `IsRetryable` comment was inconsistent → reworded.
  - **Documented:** the new locking order, the backfill as a one-off history edit, and the missing snapshot format version.
  - **Not changed:** the 1213 mapping, because a real 1213 was observed in this session.
