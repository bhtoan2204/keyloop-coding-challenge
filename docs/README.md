# Design & Plan — Intelligent Inventory Dashboard (Scenario B, backend)

Status: **DRAFT — awaiting design review. No application code is to be written until the decisions below are signed off.**

> Sources: `KeyloopCodingChallange.pdf` (Scenario B, Part 1/2, deliverables, evaluation framework) and the
> engineering brief that layers the ES/CQRS/CDC architecture on top. [01 §1](01-requirements-analysis.md)
> separates what the PDF requires from what we chose to add.

## Documents

| # | Document | Covers (brief §10 item) |
|---|----------|-------------------------|
| 01 | [Requirements analysis](01-requirements-analysis.md) | Acceptance criteria, mandatory vs additional, ambiguities & assumptions (1, 2) |
| 02 | [Architecture review](02-architecture-review.md) | Proposed architecture critique, risks, existing scaffolding review, ADRs (3) |
| 03 | [Domain model](03-domain-model.md) | Aggregate boundary, invariants, commands, events, aging rule (4, 5) |
| 04 | [Event store](04-event-store.md) | MySQL schema, optimistic concurrency, retries, idempotency (6) |
| 05 | [Read model & projection](05-read-model-and-projection.md) | PostgreSQL schema, CDC, Kafka ordering, idempotency, rebuild, consistency (7, 8) |
| 06 | [API contracts](06-api-contracts.md) | REST endpoints, payloads, errors, pagination (9) |
| 07 | [Implementation plan](07-implementation-plan.md) | Repository layout, milestones, test acceptance criteria, critical path (10, 11, 12) |
| 08 | [Observability strategy](08-observability.md) | Logs, metrics, traces, health |

Final deliverables still to be produced during implementation (PDF "Deliverables & Submission"):
System Design Document (consolidated from 01–05 + 08, including the PDF-required GenAI-in-design section),
README with build/run/test **and the AI Collaboration Narrative as a dedicated README section**, OpenAPI file +
cURL examples, demo script and video outline. Narrative content comes only from `docs/ai-log.md` (07 §2, M0–M7).

## Decisions that need sign-off

| ID | Decision | Default proposed | Where |
|----|----------|------------------|-------|
| D1 | Replace GORM + SQLite scaffolding with `database/sql` (MySQL) + `pgx` (Postgres) and real-database tests | Replace | 02 §4 |
| D2 | Drop snapshots, the `es_aggregate` version table, Redis/cache stubs, outbox projector, reflection-based serializer | Drop | 02 §4 |
| D3 | A new action may only be **proposed** for a vehicle that is aging at the business date | Enforce in domain | 01 §3 A6 |
| D4 | Active-VIN uniqueness enforced via a write-side reservation table in the same transaction as the append | Yes | 04 §5 |
| D5 | Kafka offsets stored in Postgres in the same transaction as the projection write | Yes | 05 §4 |
| D6 | Projector repairs version gaps by reading the missing events from MySQL (read-only user) | Yes | 05 §4.3 |
| D7 | One binary, two long-running processes (`api`, `projector`) plus `migrate` / `rebuild` subcommands | Yes | 02 §3 |
| D8 | Jaeger + Prometheus containers only behind an opt-in Compose profile | Opt-in | 08 |
| D9 | Action vocabulary: a fixed enum + free-text note; lifecycle Proposed → Revised* → Cancelled / closed by sale | Yes | 03 §4 |
| D10 | Add an inventory **summary** endpoint (in-stock count, aging count, aging-without-action count) to back the PDF's "prominently display" aging stock | Yes | 06 |
| D11 | Keep a running `docs/ai-log.md` (prompt intent → AI output → what was verified/rejected/corrected) from M0 onward as the factual source for the AI narrative | Yes | 07 §2 |
