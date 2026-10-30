# 08 — Observability Strategy

Goal: an operator can answer three questions without attaching a debugger. *Is the API healthy? Is the read model right? Why did this command fail?*

Since Phase 7 there is one process (`api`) and one database. The read model is updated in the command's transaction, so there is no projection lag to measure. (History: Phases 5–6 also ran a Kafka projector with its own `/readyz`, `/status` and `inventory_projection_*` metrics; they were removed with it.)

## As implemented

- **Logs:** JSON `slog` to stdout.
  - One line per HTTP request: request ID, route pattern, status, duration, sanitised actor.
  - Errors with their problem code; 5xx carry only the `request_id` in the body, the full error is in the log.
  - WARN when a command is retried after a retryable save failure.
  - `rebuild-projections` logs events read, events applied and duration; a failure is logged with its error and the process exits non-zero.
- **Correlation:** `X-Request-Id` is accepted or generated, echoed, written into event metadata as `correlation_id`, and returned in action history and problem bodies. A valid W3C `traceparent` is stored in event metadata too.
- **Metrics** (`GET /metrics` on the API):
  - `inventory_http_requests_total{method,route,status}`;
  - `inventory_http_request_duration_seconds{method,route}`: for commands it includes the projection, which runs in the same transaction;
  - `inventory_http_problems_total{route,code}`: command success and failure by business outcome (e.g. `VEHICLE_NOT_AGING`, `CONCURRENCY_CONFLICT`, `UNAVAILABLE`);
  - Go runtime and process metrics.
  - Labels are bounded: route patterns, and non-standard methods fold into `OTHER`.
- **Health:**
  - `/healthz`: liveness only;
  - `/readyz`: pings the writer pool and the read-only pool (2 s timeout); 503 without detail when either fails;
  - Compose health checks use the binary's `healthcheck URL` subcommand (distroless image, no shell).
- **Read-model integrity:** `read_model.projection_aggregate` holds the applied version per vehicle, equal to `event_store.es_aggregate.version` after every commit; `read_model.projection_run` records each rebuild (kind, start, finish, events read and applied).

## Not implemented

- OpenTelemetry spans and export (`traceparent` is stored but no spans are exported);
- event-store / projection latency histograms and conflict-retry counters (the HTTP duration and `inventory_http_problems_total` cover them coarsely);
- the Jaeger/Prometheus Compose profile (D8) and alert rules.

## Suggested alerts (not configured)

- 5xx rate above 1 % for 5 minutes (`inventory_http_requests_total{status=~"5.."}`);
- `inventory_http_problems_total{code="UNAVAILABLE"}` increasing: database unreachable or lock contention on every retry;
- `inventory_http_problems_total{code="CONCURRENCY_CONFLICT"}` increasing: retries exhausted under contention;
- `/readyz` failing.

## Planned tracing design (not built)

- HTTP server span, then a command-handler span, then database spans for load and save.
- Exporter: OTLP/gRPC through the standard `OTEL_*` variables; a no-op without an endpoint.
