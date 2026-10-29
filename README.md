# Keyloop Scenario B — Intelligent Inventory Dashboard (backend)

A Go backend for a dealership inventory dashboard:

- it lists each dealership's vehicles with their **age in days**;
- it identifies **aging stock (in stock for more than 90 days)** automatically and prominently;
- it lets a manager **log, revise and cancel a proposed action** for each aging vehicle, with a full **history**.

Architecture: Event Sourcing + CQRS.

- **Write side:** commands append events to a **MySQL** event store with optimistic concurrency.
- **Change feed:** **Debezium** streams the event table through **Kafka**.
- **Read side:** a projector keeps a **PostgreSQL** read model that serves the queries.

| Read this | For |
|---|---|
| [docs/system-design.md](docs/system-design.md) | System Design Document (architecture, consistency, concurrency, failure handling, security, trade-offs, GenAI) |
| [api/openapi.yaml](api/openapi.yaml) | API contract (OpenAPI 3.1); also served at `/openapi.yaml` and as Swagger UI at `/docs` |
| [docs/curl-examples.md](docs/curl-examples.md) | Copy-paste requests for every endpoint and error |
| [docs/demo.md](docs/demo.md) | Demo script and 5–10 minute video outline |
| [docs/ai-log.md](docs/ai-log.md) | Factual log of the AI collaboration (source of the narrative below) |
| `docs/01`–`08` | Detailed requirements, domain, event store, read model, API and observability notes |

## Requirements coverage

| Keyloop requirement | Implementation |
|---|---|
| Inventory list per dealership with make, model, age | `GET /v1/dealerships/{id}/vehicles` → `make`, `model`, `age_days` on every row |
| Filter by make, model, age | `?make=&model=&min_age_days=&max_age_days=` (plus `status`, `sort`, keyset pagination) |
| Aging stock identified automatically | `is_aging` = in stock **and more than 90 days** at the dealership's business date; computed per request, never stored. Exactly 90 is not aging. |
| …and displayed prominently | `?aging=true`, oldest-first default sort, `GET …/inventory/summary` (in stock / aging / aging without action) |
| Log and persist a status / proposed action per aging vehicle | `POST …/actions`, `PUT …/actions/{actionId}`, `POST …/actions/{actionId}/cancel`, `POST …/sale`; events in MySQL |
| Action history | `current_action` on every read; `GET …/vehicles/{id}/actions` (actor, correlation ID, timestamps) |
| REST API + persistent database + OpenAPI / cURL | 5 commands, 4 queries, problem+json errors, `api/openapi.yaml`, `docs/curl-examples.md` |

## Run with Docker (the whole system)

Requirements:

- Docker with Compose v2, or Podman with podman-compose;
- `make` and `curl`;
- Go 1.26 only to run the tests;
- free local ports 3306, 5432, 9092, 8083, 8080 and 8081.

```bash
cp .env.example .env
# Edit .env: replace every change-me value with a password made of letters and digits
# (the grant scripts substitute it with sed, so avoid / & and ').

make stack-up      # build the image, start MySQL, PostgreSQL, Kafka, Kafka Connect,
                   # migrate, create the restricted accounts, create the topic,
                   # register the Debezium connector, start the API and the projector,
                   # then wait until both report ready
make e2e           # optional: the full business flow against the running stack (Go needed)
```

- **API:** `http://127.0.0.1:8080`, with Swagger UI at `/docs`.
- **Projector probes:** `http://127.0.0.1:8081/readyz`, `/status` and `/metrics`.
- **Try it:** [docs/curl-examples.md](docs/curl-examples.md).
- **Logs:** `make stack-logs`.
- **Stop:** `make stack-down` keeps the data. To wipe the volumes as well, run `docker compose down -v`.

`make stack-up` runs the one-shot init services one at a time, which works with Docker Compose and podman-compose alike. With Docker Compose v2, `docker compose up -d --build` does the same through `depends_on` conditions. That path is unverified: only podman-compose was available here, and podman cannot start a container whose `depends_on` target has already exited. Every init step is idempotent, so `make stack-up` can be re-run.

The image (`Dockerfile`):

- a multi-stage build into `distroless/static`, 26 MB;
- runs as `nonroot`;
- contains no credentials;
- holds one binary with the subcommands `api`, `projector`, `migrate`, `sync-projections`, `rebuild-projections` and `healthcheck`.

Both processes shut down gracefully on SIGTERM:

- the API finishes in-flight requests;
- the projector finishes the record in progress, commits its offset and leaves the consumer group.

## Run on the host (development)

```bash
make up && make migrate && make mysql-grants pg-grants   # MySQL + PostgreSQL, schema, restricted accounts
make cdc-up && make debezium-grants connector            # Kafka + Connect, Debezium account, connector
make run-api                                             # terminal 1
make run-projector                                       # terminal 2
```

`make sync-projections` and `make rebuild-projections` bring the read model up to date from MySQL (recovery). Both are safe while the projector runs.

## Test

```bash
make check              # gofmt, go vet (all build tags), unit tests, race detector
make test-integration   # real MySQL 8.4 and PostgreSQL 17 (needs MYSQL_TEST_DSN, PG_TEST_DSN; see .env.example)
make test-pipeline      # real MySQL + Debezium + Kafka + PostgreSQL (also KAFKA_TEST_BROKERS, CONNECT_TEST_URL)
make e2e                # business flow against a running stack (E2E_BASE_URL, default http://127.0.0.1:8080)
```

Integration, pipeline and E2E tests **fail rather than skip** when their infrastructure is missing.

What the suites cover:

- **Domain rules:** every invariant, with an accept and a reject case.
- **Aging boundaries:** 89/90/91 days, leap year, year boundary, time zones, DST.
- **Event history:** replay and snapshots.
- **MySQL concurrency:** exactly one winner, contiguous versions.
- **Error and HTTP mapping.**
- **Projection:** idempotency, gap handling and rebuild equality.
- **Change feed:** duplicate delivery, crashes before and after the PostgreSQL commit, connector restart, initial snapshot with existing history, bootstrap while writing, rebuild during live consumption, two consumers sharing partitions, truncate detection.
- **The OpenAPI contract** against the routes.
- **Package dependency direction.**

The results recorded at submission are in the Phase 6 entry of [docs/ai-log.md](docs/ai-log.md).

## Project layout

```
api/                          OpenAPI 3.1 contract (embedded and served by the API)
cmd/                          one binary: api, projector, migrate, sync/rebuild-projections, healthcheck
deploy/                       SQL grants (MySQL app + Debezium, PostgreSQL reader), Debezium connector config
internal/domain/              VehicleInventoryAggregate, events, Date and aging rule (no I/O)
internal/application/         command (write use cases), query (read use cases), messaging (change-feed consumer)
internal/infrastructure/      eventstore (MySQL), viewstore + projection (PostgreSQL), kafka, referencedata
internal/presentation/        router + commands, queries, probes, apidocs over the shared httpkit
internal/pipeline/, e2e/      real-infrastructure and end-to-end test suites
package/eventsourcing/        the event-sourcing framework the project started from (extended, not replaced)
docs/                         design documents, curl examples, demo, AI log
```

## Key behaviours and limitations

- **Consistency:**
  - commands answer with the **committed** write-side state;
  - queries read the **eventually consistent** read model, about 0.5 s behind on the development machine;
  - compare `version` to detect lag;
  - aging is computed per request, so it is never stale.
- **Concurrency:**
  - per-vehicle optimistic concurrency, with up to 3 server-side retries on fresh state;
  - optional `If-Match: "<version>"` → `412 VERSION_MISMATCH`.
- **Not implemented, all documented:**
  - authentication: `X-Actor-Id` is a demo-only, unauthenticated identity used for audit, not authorization;
  - `Idempotency-Key`;
  - VIN uniqueness among in-stock vehicles;
  - tracing export;
  - a non-superuser PostgreSQL role for the projector (it writes as the image-created superuser).
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

The developer's interventions are recorded too:

- rejecting the rewrite;
- fixing a TLS proxy problem that blocked image pulls;
- approving each phase;
- committing each phase.
