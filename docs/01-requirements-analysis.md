# 01 — Requirements Analysis

## 1. Acceptance criteria

Sources: `KeyloopCodingChallange.pdf` (Scenario B, backend layer chosen) and the engineering brief. Every B/T item maps to at least one automated test in [07](07-implementation-plan.md).

### What the PDF actually says (verbatim intent)

> **Task:** give dealership managers a **real-time overview** of their vehicle stock.
> 1. *Inventory Visualization*: display a filterable list of **all vehicles in a dealership's inventory** (e.g. filter by make, model, age).
> 2. *Aging Stock Identification*: **automatically identify and prominently display** "aging stock" (in inventory for **>90 days**).
> 3. *Actionable Insights*: allow a manager to **log and persist a status or proposed action for each aging vehicle** (e.g. "Price Reduction Planned").
>
> **Backend choice:** expose a RESTful API, use a persistent database, and mock the client with a test harness, **cURL examples** or an **OpenAPI** contract. Consider scalability, performance, reliability, maintainability, observability.

### Business (Scenario B, PDF-mandatory)

| ID | Acceptance criterion | PDF source |
|----|----------------------|------------|
| B1 | `GET` a dealership's inventory list. Every row includes **make, model and vehicle age in days**. | Req 1 |
| B2 | The list can be filtered by **make**, **model** and **age** (min/max days), scoped to one dealership. | Req 1 ("e.g." means these three at minimum) |
| B3 | Every in-stock vehicle whose age is **> 90 calendar days** is flagged `is_aging = true` with no manual step and no scheduled job. Exactly 90 days is **not** aging. | Req 2 "automatically identify" |
| B4 | Aging stock is **prominent**: an `aging=true` filter, the default sort puts the oldest first, and a **summary** endpoint returns in-stock, aging and aging-without-action counts for dashboard tiles (D10). | Req 2 "prominently display" (backend interpretation) |
| B5 | A manager can **log** a status/proposed action for an aging vehicle. It is **persisted** (survives restarts) and is returned with the vehicle in later list/detail reads. "Price Reduction Planned" is expressible as `action_type=PRICE_REDUCTION`, optionally with a note. | Req 3 |
| B6 | The current action for a vehicle and its **history** of changes can be retrieved. | Req 3 "log" (implies a trail; our interpretation) |
| B7 | "Real-time": a change is visible in the read API within **≤ 2 s (p95)** under demo load, measured by `projection_lag_seconds`. Aging needs no refresh because it is computed per request. | Task statement |

### Deliverables (PDF "Deliverables & Submission")

| ID | Required artifact |
|----|-------------------|
| P1 | **System Design Document** with: architecture diagram, role of each component, data-flow explanation, technologies **with justifications**, observability strategy, **a dedicated section on how GenAI assisted the design phase**, and documented assumptions (PDF "Note on Ambiguity"). |
| P2 | Git repo with `README.md`: build, run and test instructions **and a dedicated "AI Collaboration Narrative" section in the README**: strategy for guiding the AI, process for verifying and refining its output, and how final quality was ensured. |
| P3 | A suite of tests that validate the core business logic. |
| P4 | 5–10 min video: intro + scenario, design & implementation highlights, AI collaboration story (1–2 min), demo, learnings/challenges. |

Evaluation dimensions (PDF): design clarity and foresight · technical execution and testing · **AI engineering & verification (a "primary evaluation criterion")** · communication. Consequence for the plan: verification evidence has to be captured *while* working (D11), not reconstructed at the end.

### Technical (engineering brief, self-imposed; not required by the PDF)

The PDF does not require ES, CQRS, Kafka or Debezium. These come from our brief, so the System Design Document must **justify** each one against the PDF's "build for the future" qualities (02 §1), not just list it.

| ID | Acceptance criterion |
|----|----------------------|
| T1 | REST API with an OpenAPI 3.x specification that matches the implementation (enforced by a contract test). |
| T2 | Persistent databases: MySQL 8 (event store, write side) and PostgreSQL (read model), both on Docker volumes. |
| T3 | Event sourcing: the vehicle state is rebuilt only by replaying events. Commands produce events and never update current-state rows on the write side. |
| T4 | Optimistic concurrency is enforced **by the database** (unique `(aggregate_id, aggregate_version)`). Multi-event commands are appended atomically. |
| T5 | CDC: Debezium captures committed inserts from the event table and publishes them to Kafka. |
| T6 | The projection consumer keeps per-aggregate order, ignores duplicates, detects gaps and out-of-order versions, commits state and progress atomically, recovers after restart, and can do a full rebuild from MySQL without needing Kafka retention. |
| T7 | Aging is computed at query time from `stocked_on` and the dealership's business date. No `days_in_stock` column is stored. |
| T8 | Sorting and pagination: default page size 20, maximum 100, keyset cursor. |
| T9 | Structured logs, OpenTelemetry tracing, Prometheus metrics. |
| T10 | Unit tests plus integration tests against real MySQL/Postgres, including a concurrency test. |
| T11 | `docker compose up` starts the full system. The README documents build, run and test. |
| T12 | Docs: System Design (P1), diagrams, observability strategy, AI Collaboration Narrative **as a README section** (P2), demo instructions and video outline (P4), cURL examples. |

## 2. Mandatory vs additional

Three tiers: **PDF-mandatory** (B1–B7, P1–P4) → **brief-mandatory** (left column below, our own architectural commitment) → **additional** (right column, cuttable).

| Brief-mandatory (on top of the PDF) | Additional engineering (the solution must not depend on it) |
|---|---|
| Inventory list, filters, age, aging flag, summary, actions + history | `Idempotency-Key` replay store |
| ES aggregate, event store, OCC, CDC → Kafka → projector | `If-Match`/ETag concurrency at the HTTP layer |
| Projection idempotency, gap detection, rebuild | `as_of` business-date override on queries (demo/testing) |
| Keyset pagination, indexes | Active-VIN uniqueness reservation |
| OpenAPI, Docker Compose, tests, docs | Jaeger/Prometheus containers (opt-in profile) |
| Logs, traces, metrics (instrumentation in code) | OpenAPI contract test, `VehicleSold` endpoint (demonstrates "active" inventory) |

`VehicleSold` sits close to mandatory. Without it, "*active* inventory" cannot be shown. It is kept small.

## 3. Ambiguities and assumptions

| # | Ambiguity | Assumption (to be confirmed in review) |
|---|-----------|----------------------------------------|
| A1 | What is "the dealership's business date"? | The current calendar date in the dealership's IANA time zone (e.g. `Europe/London`). Each dealership has a `timezone` in reference data. Example: at 2026‑10‑29T14:00Z it is already 2026‑10‑30 for `Australia/Sydney`. |
| A2 | How are days counted? | `age_days = business_date − stocked_on` (calendar-date difference, DST-safe because it uses dates, not instants). The stock-in day is day 0. **Aging ⇔ status = IN_STOCK ∧ age_days > 90.** Stocked 2026‑07‑30: 2026‑10‑28 → 90 (not aging), 2026‑10‑29 → 91 (aging). |
| A3 | What is "active inventory"? | Status `IN_STOCK`. `SOLD` vehicles are excluded by default (`status=ALL` shows them). Sold vehicles have a frozen age (`sold_on − stocked_on`) and are never aging. Reserved and in-transit states are not modelled. |
| A4 | Where does inventory data come from? | There is no DMS integration. Vehicles enter through `POST …/vehicles` (StockVehicle) and a seed script. `stocked_on` is a business fact supplied by the caller and may be in the past (which is how the demo shows aging stock). It must not be in the future in dealership-local time. |
| A5 | What does "status or proposed action" mean? (PDF example: "Price Reduction Planned") | One concept, not two fields: the PDF example is a *planned action phrased as a status*. A vehicle has **at most one active action** (type + note). The manager can propose it, revise it or cancel it. A sale closes it. The full history is kept. "Completed/executed" action states are out of scope (open question Q1). |
| A6 | Can actions be recorded on non-aging vehicles? | **No** for *propose*: the requirement says "for each aging vehicle", and the domain checks it with the business date. Revise and cancel stay allowed while the vehicle is in stock. Changing this rule later is a one-line change. (Decision D3) |
| A7 | Multi-tenancy | Every read and write is scoped by `dealership_id` in the URL. A vehicle that belongs to another dealership returns 404. |
| A8 | Authentication / "manager" role | Out of scope. The caller identity comes from an `X-Actor-Id` header, is required on commands and is recorded in event metadata. It is documented as a stand-in for real auth. |
| A9 | Can the same VIN be re-stocked after sale (trade-back)? | Yes. A re-stock is a **new aggregate** (new `vehicle_id`). Only one `IN_STOCK` vehicle per VIN may exist at a time (D4). |
| A10 | Can a wrong stock-in date be corrected? | Not supported now. The design extension is a `VehicleStockInDateCorrected` event. |
| A11 | Age filter semantics | `min_age_days` and `max_age_days` are inclusive integer bounds. `aging=true` is the same as `status=IN_STOCK ∧ min_age_days=91` and combines with other filters by intersection. |
| A13 | "Real-time overview" with a CQRS read model | Interpreted as near-real-time: an end-to-end p95 target of ≤ 2 s (B7). Command responses return write-side state immediately, so the acting manager never sees lag on their own change. |
| A14 | "Prominently display" for a backend-only submission | A backend cannot render prominence. It can make prominence cheap for any UI: an `is_aging` flag on every row, an `aging=true` filter, oldest-first default sort, and a summary endpoint with counts (B4). |
| A12 | Scale | Hundreds to low thousands of vehicles per dealership and tens of dealerships. Indexes are designed for this. Hot-path reads are always single-dealership. |

## 4. Open questions (non-blocking; defaults above apply)

- Q1: Should an action be marked *completed* (e.g. "price reduced") separately from the vehicle being sold?
- Q2: Should the aging threshold (90) be configurable per dealership? The default is a global constant. The design allows a per-dealership column.
- Q3: Should the list include a total count? The default is no, because keyset pagination makes counts an extra query. It can be added as `include_total=true`.
