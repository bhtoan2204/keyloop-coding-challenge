# 08 — Observability Strategy

> **As implemented (Phase 6).** The plan below was the target; this is what exists:
> - **Logs:** JSON `slog`. One line per HTTP request (request ID, route pattern, status, duration, sanitised actor). Projector lifecycle, gap repairs and poison records (ERROR).
> - **Metrics** (`/metrics` on the API and the projector):
>   - `inventory_http_requests_total{method,route,status}`, `inventory_http_request_duration_seconds{method,route}` and `inventory_http_problems_total{route,code}`. The last gives command success/failure by business outcome; there is no separate `inventory_commands_total`.
>   - `inventory_projection_{events_applied,duplicates_skipped,gap_events_repaired,record_failures}_total`, `inventory_projection_ready`, `inventory_projection_phase{phase}`, `inventory_projection_partition_halted{topic,partition}`, `inventory_projection_partition_last_offset` and `inventory_projection_last_change_lag_seconds`. The lag is apply time minus the MySQL commit time reported by Debezium, with ms resolution for streamed records; it replaces the planned `projection_lag_seconds`, which was based on `created_at` with 1 s resolution.
>   - Go runtime and process metrics.
> - **Not implemented:**
>   - per-partition lag;
>   - event store and apply latency histograms;
>   - conflict-retry counters;
>   - OpenTelemetry spans (`traceparent` is accepted and stored in the event metadata, but no spans are exported);
>   - the Jaeger/Prometheus Compose profile (D8);
>   - alert rules.
> - **Health:** API `/readyz` checks MySQL and PostgreSQL. Projector `/readyz` is 503 while bootstrapping, while the broker is unreachable or while a partition is halted; `/status` gives the detail. Compose health checks use the binary's `healthcheck` subcommand (distroless image).

Goal: an operator can answer three questions without attaching a debugger. *Is the API healthy? How far behind is the read model? Why did this command or event fail?*

## Logs (`log/slog`, JSON to stdout)

- One logger is built in `main` and passed down. A request- or record-scoped logger travels in `context`.
- Standard fields: `service` (`api|projector|rebuild`), `trace_id`, `span_id`, `request_id`, `dealership_id`, `aggregate_id`, `version`, `event_id` (`es_event.id`), `event_type`, `kafka.partition`, `kafka.offset`, `error.code`.
- Levels: `INFO` is one line per request (method, route template, status, duration) and per projector lifecycle event. `WARN` covers conflict retries, duplicates skipped in bulk and gap repairs. `ERROR` covers 5xx, halted partitions and rebuild failure. No PII beyond the VIN. Request bodies are not logged.

## Metrics (Prometheus, `/metrics`)

| Metric | Type | Labels | Why |
|---|---|---|---|
| `http_server_request_duration_seconds` | histogram | `route`, `method`, `status` | RED for the API |
| `inventory_commands_total` | counter | `command`, `outcome` (`ok`, `rejected`, `conflict`, `error`) | business and concurrency health |
| `inventory_command_conflict_retries_total` | counter | `command` | OCC contention |
| `eventstore_append_duration_seconds` | histogram | — | write-path latency |
| `projection_events_applied_total` | counter | `event_type` | throughput |
| `projection_duplicates_skipped_total` | counter | — | at-least-once behaviour visible |
| `projection_gaps_repaired_total` | counter | — | should be 0; alert if > 0 |
| `projection_halted` | gauge | `partition` | page on 1 |
| `projection_lag_seconds` | gauge | `partition` | now − `es_event.created_at` of the last applied event (1 s resolution, R14) |
| `projection_apply_duration_seconds` | histogram | — | PG tx latency |

Kafka consumer-group lag is also visible in Kafka tooling because offsets are best-effort committed to Kafka (05 §4.1).

Suggested alerts: `projection_halted == 1` for 1 minute, `projection_lag_seconds > 30` for 5 minutes, 5xx rate > 1 % for 5 minutes, `projection_gaps_repaired_total` increase.

## Traces (OpenTelemetry)

- HTTP server span, then a command-handler span, then MySQL spans (`eventstore.load`, `eventstore.append`).
- The W3C `traceparent` is written into event **metadata**. The projector starts its span with a **span link** to that context, not a parent: CDC adds an unbounded async delay, and a link keeps the API trace short while staying navigable.
- Exporter: OTLP/gRPC, set through standard `OTEL_*` env vars. With no endpoint configured, tracing is a no-op (no extra infrastructure required). With the Compose profile `observability`, it sends to Jaeger, and Prometheus scrapes both processes (D8).

## Health

- `/healthz` reports liveness only.
- `/readyz` checks dependencies: DB pings for the API. For the projector it checks Kafka connectivity, Postgres, and that no partition is halted.
- Compose uses healthchecks for startup ordering: MySQL → migrate → Connect → connector registration → API and projector.
