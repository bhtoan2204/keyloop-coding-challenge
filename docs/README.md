# Design & Plan — Intelligent Inventory Dashboard (Scenario B, backend)

Status: **Phase 7 (PostgreSQL-only: event store and read model in one database, updated in one transaction) implemented — uncommitted, awaiting developer review. Phases 1–6 and the final hardening are committed. The submission entry points are the [README](../README.md), the [System Design Document](system-design.md), the [OpenAPI contract](../api/openapi.yaml), [curl examples](curl-examples.md) and the [demo outline](demo.md).** The numbered documents below are the working design notes; where they differ from the code, the code and the "as implemented" sections win.
Target completion of all deliverables: **Wednesday 4 November 2026.**

> Sources: `KeyloopCodingChallange.pdf` (Scenario B, Part 1/2, deliverables, evaluation framework) and the
> engineering brief that layers the ES/CQRS/CDC architecture on top. [01 §1](01-requirements-analysis.md)
> separates what the PDF requires from what we chose to add. In Phase 7 the developer decided to drop the CDC part
> (MySQL → Debezium → Kafka → projector) and keep ES/CQRS on one PostgreSQL database (D16).

## Engineering direction

**Preserve and incrementally improve the existing source code** (baseline commit `7d04502`).
The existing event-sourcing framework (`package/eventsourcing`), GORM event store
(`internal/infrastructure/eventstore`), snapshot support, aggregate-version CAS, serializer registry
and projection abstractions are kept and extended. Refactoring is allowed where a test or a concrete
technical limitation demands it, and each such refactor is recorded in the doc that motivates it.

An earlier revision of these docs (commit `dde142b`) recommended replacing most of that code. That
recommendation was **rejected by the developer** and is reversed below (D1, D2). See [ai-log.md](ai-log.md).

## Documents

| # | Document | Covers |
|---|----------|--------|
| 01 | [Requirements analysis](01-requirements-analysis.md) | Acceptance criteria, Keyloop-mandatory vs self-imposed vs optional, assumptions |
| 02 | [Architecture review](02-architecture-review.md) | Codebase inventory (existing/partial/missing), reuse plan, risks, ADRs; §9 the Phase 7 decision |
| 03 | [Domain model](03-domain-model.md) | `VehicleInventoryAggregate` on the existing `AggregateRoot`, invariants, events |
| 04 | [Event store](04-event-store.md) | §9 current PostgreSQL `event_store` schema, write path, errors, migrations, roles; §1–8 the earlier MySQL store (superseded) |
| 05 | [Read model & projection](05-read-model-and-projection.md) | §8 current transactional projection, rebuild, consistency, §9 Phase 7.1 package structure; earlier CDC design (superseded) |
| 06 | [API contracts](06-api-contracts.md) | REST endpoints, payloads, errors, pagination |
| 07 | [Implementation plan](07-implementation-plan.md) | Minimal-change roadmap to 4 Nov, critical path, cut list, Phase 7 |
| 08 | [Observability strategy](08-observability.md) | Logs, metrics, traces, health |
| — | [System Design Document](system-design.md) | The submission SDD: architecture, flows, consistency, concurrency, failures, security, trade-offs, GenAI |
| — | [curl examples](curl-examples.md) | Every endpoint and error, copy-paste ready |
| — | [Demo / video outline](demo.md) | 5–10 minute walkthrough script |
| — | [AI collaboration log](ai-log.md) | Factual record of AI contributions, corrections and verification |

## Decisions

IDs are stable. Reversed or downgraded decisions keep their ID so the history stays traceable.

| ID | Decision | Status | Where |
|----|----------|--------|-------|
| D1 | ~~Replace GORM + SQLite with `database/sql`/`pgx`~~ → **Keep GORM for the event store.** Keep the SQLite tests as fast unit-level tests and add database-backed integration tests. (The GORM MySQL driver added in Phase 2 was replaced by the PostgreSQL driver in Phase 7, D16.) | **Reversed** | 02 §4, 04 |
| D2 | ~~Drop snapshots, `es_aggregate`, serializer, outbox, Redis/cache stubs~~ → **Keep all.** Snapshots are **disabled for `VehicleInventoryAggregate`** (interval 0) because of a verified state-loss hazard (04 §7). The outbox, cache and Redis stubs stayed in place, unwired, until the developer removed them as dead code after Phase 7.1. | **Reversed** | 02 §4, 04 §7 |
| D3 | A new action may only be **proposed** for a vehicle that is aging at the business date | Proposed | 01 A6, 03 |
| D4 | Active-VIN uniqueness reservation table | **Cut** (not implemented; disclosed) | 04 §6 |
| D5 | Projection progress = the per-aggregate version stored in the same Postgres tx as the state (**mandatory**). Storing Kafka offsets in Postgres too is **optional**. | Downgraded; Kafka part **superseded** (D16) | 05 §4 |
| D6 | Version gaps: **detect + halt** (mandatory). Automatic repair from MySQL is **optional**. | Implemented in Phase 5; **superseded** in Phase 7: a gap now fails the command's save or the rebuild (D16) | 05 §7.5, §8 |
| D7 | One binary (`cmd/main.go`) with subcommands `api`, `projector`, `migrate`, `rebuild` | **Implemented**; since Phase 7: `api`, `migrate`, `rebuild-projections`, `healthcheck` | README |
| D8 | Jaeger + Prometheus containers only behind an opt-in Compose profile | **Not done**: `/metrics` endpoints only, no extra containers | 08 |
| D9 | Action vocabulary: fixed enum + note; Proposed → Revised* → Cancelled / closed by sale | Proposed | 03 |
| D10 | Inventory **summary** endpoint backing the PDF's "prominently display" | Proposed | 06 |
| D11 | Maintain [ai-log.md](ai-log.md) from Phase 0 onward as the only source for the AI narrative | **Active** | 07 |
| D12 | Keep reflection-derived type names (`VehicleInventoryAggregate`, `VehicleStockedEvent`, …) as the **frozen** stored names, pinned by the existing aggregate test. No renaming. | Proposed | 03 §5, 04 §4 |
| D13 | Evolve the schema **additively** through a versioned extension of the existing `repos.Migrate`: `es_event.aggregate_type` and `es_event.schema_version` with defaults | Proposed | 04 §4 |
| D14 | Build the rebuild/catch-up reader (`es_event` → Postgres) **before** Kafka, so the business flow is testable end to end early. Debezium/Kafka then replaces the transport for live updates. | Done; Kafka part **superseded** (D16) | 05 §4.4, 07 |
| D15 | Keep the `es_aggregate` CAS **and** the unique `(aggregate_id, version)` as the OCC mechanism. A possible MySQL deadlock (H1) is to be verified, not assumed. | Implemented (H1 confirmed and fixed on MySQL); kept on PostgreSQL | 04 §5, §9 |
| D16 | **PostgreSQL only** (Phase 7): one database with schemas `event_store` and `read_model`; each command appends its events and updates the read model in one transaction; no MySQL, Debezium, Kafka or projector process. The developer decided this because the CDC pipeline's operational cost outweighed its benefit at this scale and immediate read-after-write matters for the manager UI. | **Implemented** | 02 §9, 04 §9, 05 §8, system-design |

## Open decisions for Phase 1 sign-off

See [07 §6](07-implementation-plan.md#6-risks-and-open-decisions-for-phase-1).
