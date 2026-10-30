# Keyloop Scenario B — Intelligent Inventory Dashboard (backend)

A Go backend for a dealership inventory dashboard:

- it lists each dealership's vehicles with their **age in days**;
- it identifies **aging stock (in stock for more than 90 days)** automatically and prominently;
- it lets a manager **log, revise and cancel a proposed action** for each aging vehicle, with a full **history**.

Architecture: Event Sourcing + CQRS on **one PostgreSQL 17 database**.

- **Write side:** commands append events to the `event_store` schema with optimistic concurrency.
- **Read side:** the same transaction applies those events to the `read_model` schema that serves the queries. A query right after a command already sees it.
- **One process** (`inventory api`) plus one-shot init jobs (`migrate`, role grants). `rebuild-projections` recreates the read model from the event history.

Until Phase 7 the design used a MySQL event store, Debezium, Kafka and a separate projector process. The developer decided to simplify to PostgreSQL only; the reasons and trade-offs are in [docs/system-design.md §15](docs/system-design.md#15-scalability-trade-offs) and [docs/02 §9](docs/02-architecture-review.md#9-phase-7-postgresql-only).

| Read this | For |
|---|---|
| [docs/system-design.md](docs/system-design.md) | System Design Document (architecture, consistency, concurrency, failure handling, security, trade-offs, GenAI) |
| [api/openapi.yaml](api/openapi.yaml) | API contract (OpenAPI 3.1); also served at `/openapi.yaml` and as Swagger UI at `/docs` |
| [docs/curl-examples.md](docs/curl-examples.md) | Copy-paste requests for every endpoint and error |
| [docs/demo.md](docs/demo.md) | Demo script and 5–10 minute video outline |
| [docs/ai-log.md](docs/ai-log.md) | Factual log of the AI collaboration (source of the narrative below) |
| `docs/01`–`08` | Detailed requirements, domain, event store, read model, API and observability notes (02, 04, 05 and 07 keep the earlier MySQL/Kafka design as history) |

## Requirements coverage

| Keyloop requirement | Implementation |
|---|---|
| Inventory list per dealership with make, model, age | `GET /v1/dealerships/{id}/vehicles` → `make`, `model`, `age_days` on every row |
| Filter by make, model, age | `?make=&model=&min_age_days=&max_age_days=` (plus `status`, `sort`, keyset pagination) |
| Aging stock identified automatically | `is_aging` = in stock **and more than 90 days** at the dealership's business date; computed per request, never stored. Exactly 90 is not aging. |
| …and displayed prominently | `?aging=true`, oldest-first default sort, `GET …/inventory/summary` (in stock / aging / aging without action) |
| Log and persist a status / proposed action per aging vehicle | `POST …/actions`, `PUT …/actions/{actionId}`, `POST …/actions/{actionId}/cancel`, `POST …/sale`; events in PostgreSQL (`event_store.es_event`) |
| Action history | `current_action` on every read; `GET …/vehicles/{id}/actions` (actor, correlation ID, timestamps) |
| REST API + persistent database + OpenAPI / cURL | 5 commands, 4 queries, problem+json errors, `api/openapi.yaml`, `docs/curl-examples.md` |

## Run with Docker (the whole system)

Requirements:

- Docker with Compose v2, or Podman with podman-compose;
- `make` and `curl`;
- Go 1.26 only to run the tests (`go.mod` pins `toolchain go1.26.10`; the `go` command downloads it automatically);
- free local ports 5432 and 8080.

```bash
cp .env.example .env
# Edit .env: replace every change-me value with a password made of letters and digits
# (the grant scripts substitute it into SQL and refuse anything else).

make stack-up      # build the image, start PostgreSQL, run migrate, create the restricted
                   # roles, start the API, then wait until it reports ready
make e2e           # optional: the full business flow against the running stack (Go needed)
```

- **API:** `http://127.0.0.1:8080`, with Swagger UI at `/docs`, probes `/healthz`, `/readyz` and Prometheus `/metrics`.
- **Try it:** [docs/curl-examples.md](docs/curl-examples.md).
- **Rebuild the read model** (recovery): `docker compose --profile tools run --rm --no-deps rebuild`.
- **Logs:** `make stack-logs`.
- **Stop:** `make stack-down` keeps the data. To wipe the volume as well, run `docker compose down -v`.

`make stack-up` runs the one-shot init services one at a time, which works with Docker Compose and podman-compose alike. With Docker Compose v2, `docker compose up -d --build` does the same through `depends_on` conditions. That path is unverified: only podman-compose was available here, and podman cannot start a container whose `depends_on` target has already exited. Every init step is idempotent, so `make stack-up` can be re-run.

**Fresh installations only.** `migrate` creates both schemas on an empty database. A database that still has read-model tables in `public` from an earlier layout is refused with an error and left untouched (nothing is moved or dropped); start from a new volume instead. Earlier MySQL/Kafka volumes are not used and are never deleted by this project.

The image (`Dockerfile`):

- a multi-stage build into `distroless/static`;
- runs as `nonroot`;
- contains no credentials;
- holds one binary with the subcommands `api`, `migrate`, `rebuild-projections` and `healthcheck`.

On SIGTERM the API stops accepting connections and finishes in-flight requests.

## Run on the host (development)

```bash
make up && make migrate && make pg-grants   # PostgreSQL, both schemas, restricted roles
make run-api
```

`make rebuild-projections` replaces the read model with a replay of the whole event store, in one transaction. Commands that arrive meanwhile wait for it; a second concurrent rebuild is refused.

## Test

```bash
make check              # gofmt, go vet (all build tags), unit tests, race detector
make test-integration   # real PostgreSQL 17 (needs PG_TEST_DSN, an admin URL; see .env.example)
make e2e                # business flow against a running stack (E2E_BASE_URL, default http://127.0.0.1:8080)
```

Integration and E2E tests **fail rather than skip** when their infrastructure is missing. Each integration test creates and drops its own database and roles. The fast unit tests run the same schema-qualified SQL on SQLite (an attached database named `event_store`).

What the suites cover:

- **Domain rules:** every invariant, with an accept and a reject case.
- **Aging boundaries:** 89/90/91 days, leap year, year boundary, time zones, DST.
- **Event history:** replay and snapshots.
- **PostgreSQL concurrency:** exactly one winner, contiguous versions; concurrent proposals over HTTP give one 201 and 409 for the rest.
- **Atomicity:** a failed projection or a lock timeout rolls back the events, the version CAS and the read model together; nothing is reported as success.
- **Read-after-write:** a GET right after a command returns the new version; history `event_id` equals `es_event.id`.
- **Rebuild:** equals the read model the commands produced, also with commands running concurrently.
- **Migrations:** fresh layout, idempotent re-runs keeping data, concurrent callers, rollback of a failed version, newer version refused, an earlier `public` layout refused untouched.
- **Read-model repositories:** mapping and NULL/date/UTC round trips, transaction-bound writes that roll back together, list SQL composed only of bound parameters with the planned indexes usable.
- **Least privilege:** the deployed roles can do their job and nothing more.
- **Error and HTTP mapping; the OpenAPI contract against the routes; package dependency direction.**

Test results are recorded per phase in [docs/ai-log.md](docs/ai-log.md).

## Project layout

```
api/                          OpenAPI 3.1 contract (embedded and served by the API)
cmd/                          one binary: api, migrate, rebuild-projections, healthcheck
deploy/postgres/              role grants: application writer, read-only, rebuild
internal/domain/              VehicleInventoryAggregate, events, Date and aging rule (no I/O)
internal/application/         command (write use cases), query (read use cases), projection (read-model write port);
                              each declares the ports it needs
internal/infrastructure/      eventstore (event_store schema), projection (projectors, rebuild),
                              viewstore (read_model: models, repositories, migrations), referencedata
internal/presentation/        router + commands, queries, probes, apidocs over the shared httpkit
internal/e2e/                 end-to-end test suite
package/eventsourcing/        the event-sourcing framework the project started from (extended, not replaced)
docs/                         design documents, curl examples, demo, AI log
```

## Key behaviours and limitations

- **Consistency:**
  - a command appends its events and updates the read model in **one** PostgreSQL transaction;
  - commands answer with the committed state; a GET right after a command returns the same version, so its `ETag` can be sent as `If-Match` at once;
  - aging is computed per request, so it is never stale.
- **Concurrency:**
  - per-vehicle optimistic concurrency, with up to 3 server-side retries on fresh state;
  - optional `If-Match: "<version>"` → `412 VERSION_MISMATCH`.
- **Costs of the synchronous projection:** the projection runs inside each command's transaction (its cost adds to command latency), and a rebuild makes commands wait until it commits. Everything runs on one PostgreSQL primary.
- **Not implemented, all documented:**
  - authentication: `X-Actor-Id` is a demo-only, unauthenticated identity used for audit, not authorization;
  - `Idempotency-Key`;
  - VIN uniqueness among in-stock vehicles;
  - tracing export.
- **Further limitations** are listed in [docs/system-design.md §16](docs/system-design.md#16-limitations-and-future-improvements).

## AI Collaboration Narrative

This project was built with Claude Code (Claude Opus 5.5) as an implementation partner. The developer owned the direction, approvals and commits. [docs/ai-log.md](docs/ai-log.md) records every phase factually: what was delegated, what the AI got wrong, what the developer rejected, and what was verified by running commands. This narrative summarises that log.

### Strategy: how the AI was guided

- **Project memory and rules instead of long prompts.** The repository carries:
  - a `CLAUDE.md` (requirements, invariants, reuse map, definition of done);
  - rule files for architecture, Event Sourcing, testing and source organization;
  - skills for planning, testing, reviewing and security review;
  - two read-only reviewer agents (Go, and Event Sourcing/CQRS);
  - a safety hook (`.claude/hooks/guard.py`) that denies edits to `.env` files and destructive git commands (`reset --hard`, `clean -f`, force push, …) and asks the developer before any commit or push. The AI committed nothing; the developer committed every phase.
- **One phase at a time.** The AI inspected the real code, proposed a minimal-change plan and waited for approval. Then it implemented in small steps, ran the full test suite, ran an independent review, logged the result and stopped. The developer reviewed and committed each phase.
- **Preserve, don't rewrite.** The AI's first design (in the planning docs) recommended replacing the existing event-sourcing framework. That claim was made **without running the existing tests**, and the developer **rejected** it. From then on the AI was required to extend `package/eventsourcing`, the GORM event store and the projection abstractions. This is why the design keeps components that a greenfield design might not choose.

### Verification: how output was checked and refined

- **Evidence over assertion.** Every phase quotes the commands it ran (gofmt, vet, unit, race, real MySQL/PostgreSQL integration, later real Kafka/Debezium pipeline tests) and their results. Untested claims are labelled "not verified".
- **Hypotheses first.** A suspected MySQL deadlock under concurrent saves was written down as hypothesis H1. It was reproduced by new concurrency tests (error 1213), and only then fixed with a one-statement change.
- **Tests that can fail.** Important rules were checked with mutation tests: the rule is broken on purpose and a test must fail. This exposed several weak tests, which were strengthened:
  - a pagination test that passed only because of index order;
  - a run-lock test that hung instead of failing;
  - a decoder test whose input replacement silently did nothing;
  - a rollback test that a skip bug could have survived.
- **Independent review.** Read-only reviewer agents, given only the code and the base commit, found real defects the implementing session had missed:
  - an `If-Match` race that returned 409 instead of 412;
  - a Kafka retention reset that could skip a halted record;
  - Debezium silently dropping truncates of the event table by default;
  - readiness that stayed green while Kafka was unreachable.

  All were fixed with tests. The last was verified by stopping the Kafka container.
- **Behaviour-preserving refactors were proven, not assumed.** For the presentation restructuring, the old and new binaries ran side by side on the same databases. 50 requests gave identical statuses, headers and bodies.

### What the AI got wrong (and how it was caught)

- **It missed the challenge PDF at first.** It listed only tracked files; the developer pointed to the file.
- **The framework rewrite** described above.
- **A projection design that would not scale.** One advisory lock per vehicle in a rebuild would exhaust PostgreSQL's lock table. The AI caught it itself before testing.
- **A reader connection that defaulted to the schema owner's user name.** Found by the live demo.
- **A `pkill` pattern that killed the AI's own shell.**
- **A `git reset` on the index** that violated a project rule. It was restored and disclosed.
- **A Makefile variable that broke in sub-makes.** Found by the clean-start verification in Phase 6.

Each one is in the log with its fix.

### How the final result came together

- **Requirements:** the PDF's requirements were mapped to acceptance criteria first (`docs/01`). The mandatory business flow was built and tested on the write and read sides before any Kafka work, so the self-imposed CDC pipeline could be cut if time ran short.
- **Pipeline design:** Debezium/Kafka was added on the existing projection, with explicit at-least-once and bootstrap reasoning, and tested on the real stack.
- **Final phase:** packaging, the API contract, observability and documentation. A clean-copy start was run, followed by the end-to-end flow, a consumer restart, a rebuild comparison and a full restart.
- **Final hardening:** `govulncheck` findings were removed with minimum patch upgrades (Go 1.26.10, pgx 5.9.2, x/text 0.41.0, edwards25519 1.1.1). The projector moved off the PostgreSQL superuser to a restricted projection-writer role. Both were verified with the full test suites on a fresh stack.

The developer's interventions are recorded too:

- rejecting the rewrite;
- fixing a TLS proxy problem that blocked image pulls;
- approving each phase;
- committing each phase.

### Phase 7: the developer's decision to simplify

After the CDC pipeline was working and tested, **the developer decided** to drop MySQL, Debezium, Kafka and the projector process and keep Event Sourcing and CQRS on **one PostgreSQL database**. At this scale the pipeline's operational cost outweighed its benefit, and a manager who records an action should see it immediately. The AI did not propose this change. It implemented the developer's brief:

- **Kept as is:** the domain, the event names and payloads, `package/eventsourcing`, the serializer, GORM, the REST contract and the business rules.
- **Changed:**
  - the event store moved to the `event_store` schema and the read model to `read_model`;
  - each command now appends its events and updates the read model in one transaction, and the API answers only after that commit;
  - rebuilds take an exclusive lock that commands share, so a rebuild sees a consistent snapshot.
- **Removed only after the replacement was proven:** the old MySQL and pipeline tests were replaced first by PostgreSQL tests of the new guarantees (atomic rollback, immediate read-after-write, event ids in history, rebuild alongside concurrent commands, least-privilege roles, upgrade of the earlier read-model tables). The obsolete code and dependencies were deleted after that.
- **Existing history:** it was not converted automatically. An explicitly invoked import script copied the MySQL history with its ids and was checked byte for byte against history written by the pre-Phase-7 binary. The developer later removed it (Phase 7.1), together with the in-place upgrade of the earlier read model, because the assessment needs only a fresh installation.
- **AI mistakes this phase:**
  - a `pkill -f` pattern again matched the AI's own shell;
  - a compose command for the rebuild job did not work under podman-compose. It was found by the clean-copy start and corrected in the docs.

  Both are in [docs/ai-log.md](docs/ai-log.md).

### Phase 7.1: cleanup and repository structure

The developer then asked for a cleanup:

- **Removed:** the one-off MySQL import and the legacy read-model upgrade. A database with the earlier `public` layout is now refused, not adopted.
- **Ports in the application layer:** the read-model write port (`projection.Store`) and its models moved from infrastructure to `internal/application/projection`, next to `command.VehicleStore` and `query.ReadModel`.
- **Viewstore split** into:
  - persistence `models`;
  - one repository per table group, all built on the caller's transaction;
  - explicit mappers;
  - `migrations`.
- **List query:** now composed from named GORM scopes instead of concatenated predicates.

The SQL, the indexes it can use, the REST contract and the single-transaction guarantee are unchanged, and the existing tests pass. The AI's mistake this phase: a justification comment ("a `time.Time` bound to a `DATE` column could shift by a day") was disproved by its own mutation test. pgx encodes the date fields only, so the comment was corrected (see [docs/ai-log.md](docs/ai-log.md)).

