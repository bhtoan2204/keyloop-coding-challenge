# Keyloop Scenario B — Intelligent Inventory Dashboard (backend)

Go backend with Event Sourcing and CQRS:

- **Write side:** commands append events to a MySQL event store.
- **Read side:** Debezium streams the `es_event` table through Kafka into a PostgreSQL read model, which the query API serves.

Design and plan: [`docs/`](docs/README.md); the read side and change feed are in [`docs/05`](docs/05-read-model-and-projection.md).

> This README holds the operational instructions so far. The full deliverable README, including the AI Collaboration Narrative, is part of the final phase.

## Prerequisites

- Go 1.26 with cgo (SQLite-backed unit tests).
- Docker Compose, or Podman with `docker compose` (podman-compose).
- `curl` and `make`.

## Configuration

```bash
cp .env.example .env   # then replace every change-me value; .env is git-ignored
```

Every service binds to 127.0.0.1 only. Secrets come from `.env`. The Debezium password reaches Kafka Connect as an environment variable and is never stored in the connector config.

## Run the stack

```bash
make up                      # MySQL 8.4 (event store) + PostgreSQL 17 (read model)
make migrate                 # event-store and read-model migrations
make mysql-grants pg-grants  # restricted API user (MySQL) and read-only role (PostgreSQL)

make cdc-up                  # Kafka 4.3 (KRaft) + Kafka Connect with Debezium 3.6
make debezium-grants         # connector account: binlog privileges + SELECT on es_event
make connector               # register the es_event connector (idempotent)
make connector-status

make run-api                 # REST API on HTTP_ADDR (default 127.0.0.1:8080)
make run-projector           # live consumer; probes on PROJECTOR_HTTP_ADDR (default 127.0.0.1:8081)
```

A command is visible on the read side after a short delay: about 0.5 s on the development machine. The read side is eventually consistent. Compare the `version` in a command response with the `version` you read.

### Projector operations

| Need | Command |
|---|---|
| Health / readiness / progress | `curl 127.0.0.1:8081/readyz`, `curl 127.0.0.1:8081/status` |
| Catch up from MySQL (safe while the projector runs) | `make sync-projections` |
| Rebuild the read model from MySQL | `make rebuild-projections` |
| Restart the connector (resumes from its binlog offset) | `make connector-restart` |

`/readyz` returns 503 while the projector bootstraps, while Kafka is unreachable, or while a partition is halted on a record it cannot apply. Such a record is never skipped. The recovery runbook is in [`docs/05` §7.6](docs/05-read-model-and-projection.md#76-failure-handling-and-recovery-runbook).

## Test

```bash
make check              # gofmt, go vet (all build tags), go test, go test -race
make test-integration   # real MySQL + PostgreSQL; needs MYSQL_TEST_DSN, PG_TEST_DSN
make test-pipeline      # real MySQL + Debezium + Kafka + PostgreSQL; also needs KAFKA_TEST_BROKERS, CONNECT_TEST_URL
```

Integration and pipeline tests **fail rather than skip** when their infrastructure is not configured. Each pipeline test creates its own databases, connector, topic and consumer group, and removes them afterwards.
