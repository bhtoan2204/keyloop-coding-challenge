# Local values (passwords, ports, DSNs) come from .env; see .env.example.
-include .env
export

COMPOSE ?= docker compose
MYSQL_DATABASE ?= inventory
MYSQL_APP_USER ?= inventory_app
DEBEZIUM_MYSQL_USER ?= debezium
# Make-level defaults: a shell-style ${VAR:-default} inside an exported variable would be
# expanded by make (to nothing) in sub-makes such as `$(MAKE) connect-wait`.
CONNECT_PORT ?= 8083
API_PORT ?= 8080
PROJECTOR_PORT ?= 8081
CONNECT_URL ?= http://127.0.0.1:$(CONNECT_PORT)
CONNECTOR ?= es-event-cdc

.PHONY: fmt-check vet test test-race test-integration test-pipeline e2e stack-up stack-wait stack-down stack-logs check build migrate run-api run-projector sync-projections rebuild-projections up mysql-up mysql-wait mysql-down mysql-grants pg-up pg-wait pg-grants cdc-up connect-wait debezium-grants connector connector-status connector-restart

fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt needed"; exit 1; }

vet:
	go vet ./...
	go vet -tags integration ./...
	go vet -tags 'integration pipeline' ./...
	go vet -tags e2e ./...

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

## Runs the MySQL- and PostgreSQL-backed tests. Needs `make up`, MYSQL_TEST_DSN and PG_TEST_DSN.
## The tests fail (never skip) when a DSN is missing.
test-integration:
	go test -tags integration -count=1 -v ./...

## Full pipeline tests: real MySQL, Debezium (Kafka Connect), Kafka and PostgreSQL.
## Needs `make up cdc-up debezium-grants` and the DSNs plus KAFKA_TEST_BROKERS and
## CONNECT_TEST_URL (see .env.example). Each test registers its own connector.
test-pipeline:
	go test -tags 'integration pipeline' -count=1 -v -timeout 20m -run 'TestPipeline' ./internal/pipeline/...

## Business flow against a running system (docker compose stack or run-api + run-projector).
## E2E_BASE_URL defaults to http://127.0.0.1:8080.
e2e:
	go test -tags e2e -count=1 -v ./internal/e2e/...

check: fmt-check vet test test-race

## The whole system in containers: databases, Kafka, Debezium, migrations, grants,
## topic, connector, API (127.0.0.1:8080) and projector (127.0.0.1:8081).
## Runs the init jobs one by one, so it works with Docker Compose and podman-compose
## alike (podman cannot start a container whose depends_on target has exited; with
## Docker Compose v2 a plain `docker compose up -d --build` does the same).
stack-up:
	$(COMPOSE) build migrate
	$(COMPOSE) up -d mysql postgres kafka connect
	$(MAKE) mysql-wait pg-wait connect-wait
	$(COMPOSE) run --rm --no-deps migrate
	$(COMPOSE) run --rm --no-deps mysql-grants
	$(COMPOSE) run --rm --no-deps postgres-grants
	$(COMPOSE) run --rm --no-deps kafka-topic
	$(COMPOSE) run --rm --no-deps connector
	$(COMPOSE) up -d --no-deps api projector
	$(MAKE) stack-wait

## Waits until the API and the projector report ready.
stack-wait:
	@for i in $$(seq 1 90); do \
		curl -fsS -m 3 http://127.0.0.1:$(API_PORT)/readyz >/dev/null 2>&1 \
			&& curl -fsS -m 3 http://127.0.0.1:$(PROJECTOR_PORT)/readyz >/dev/null 2>&1 \
			&& { echo "api and projector are ready"; exit 0; }; \
		sleep 2; \
	done; echo "api/projector not ready; see make stack-logs"; exit 1

## Stops and removes the containers; data volumes are kept (add -v by hand to wipe them).
stack-down:
	$(COMPOSE) down

stack-logs:
	$(COMPOSE) logs -f api projector

build:
	go build -o bin/inventory ./cmd

## Applies migrations with the admin account (MYSQL_MIGRATE_DSN or root + MYSQL_ROOT_PASSWORD).
migrate:
	go run ./cmd migrate

## Serves the API: commands as the restricted MySQL app user, queries as the PostgreSQL reader.
run-api:
	go run ./cmd api

## Live projection consumer: Debezium/Kafka change feed -> PostgreSQL read model.
## Probes on PROJECTOR_HTTP_ADDR (/healthz, /readyz, /status).
run-projector:
	go run ./cmd projector

## Point-in-time read-model refresh from MySQL (recovery; the projector does this on
## startup): applies events committed since the last run, read from one consistent
## MySQL snapshot. Safe to repeat and safe while the projector runs.
sync-projections:
	go run ./cmd sync-projections

## Rebuilds the whole read model from the MySQL history in one transaction.
rebuild-projections:
	go run ./cmd rebuild-projections

up: mysql-up pg-up

## Kafka (KRaft) and Kafka Connect with Debezium. Then: `make debezium-grants connector`.
cdc-up:
	@test -n "$$DEBEZIUM_MYSQL_PASSWORD" || { echo "set DEBEZIUM_MYSQL_PASSWORD in .env"; exit 1; }
	$(COMPOSE) up -d kafka connect
	$(MAKE) connect-wait

connect-wait:
	@for i in $$(seq 1 90); do \
		curl -fsS -m 3 $(CONNECT_URL)/connectors >/dev/null 2>&1 && { echo "kafka connect is ready"; exit 0; }; \
		sleep 2; \
	done; echo "kafka connect did not become ready"; exit 1

## Creates the Debezium MySQL account (binlog + SELECT on es_event). Run after `make migrate`.
debezium-grants:
	@test -n "$$DEBEZIUM_MYSQL_PASSWORD" || { echo "set DEBEZIUM_MYSQL_PASSWORD in .env"; exit 1; }
	@sed -e "s/{{database}}/$(MYSQL_DATABASE)/g" -e "s/{{debezium_user}}/$(DEBEZIUM_MYSQL_USER)/g" \
		-e "s/{{debezium_password}}/$$DEBEZIUM_MYSQL_PASSWORD/g" deploy/mysql/debezium-grants.sql \
		| $(COMPOSE) exec -T mysql mysql -uroot -p"$$MYSQL_ROOT_PASSWORD"

## Registers (or updates) the es_event connector. Idempotent: PUT of the config.
connector:
	@curl -fsS -X PUT -H 'Content-Type: application/json' --data @deploy/debezium/es-event-connector.json \
		$(CONNECT_URL)/connectors/$(CONNECTOR)/config >/dev/null && echo "connector $(CONNECTOR) registered"

connector-status:
	@curl -fsS $(CONNECT_URL)/connectors/$(CONNECTOR)/status; echo

## Restarts the connector and its task; it resumes from its stored binlog offset.
connector-restart:
	@curl -fsS -X POST "$(CONNECT_URL)/connectors/$(CONNECTOR)/restart?includeTasks=true" >/dev/null && echo "restart requested"

mysql-up:
	$(COMPOSE) up -d mysql
	$(MAKE) mysql-wait

mysql-wait:
	@for i in $$(seq 1 60); do \
		$(COMPOSE) exec -T mysql mysqladmin ping -h 127.0.0.1 -uroot -p"$$MYSQL_ROOT_PASSWORD" --silent >/dev/null 2>&1 \
			&& { echo "mysql is ready"; exit 0; }; \
		sleep 2; \
	done; echo "mysql did not become ready"; exit 1

mysql-down:
	$(COMPOSE) down

pg-up:
	$(COMPOSE) up -d postgres
	$(MAKE) pg-wait

pg-wait:
	@for i in $$(seq 1 60); do \
		$(COMPOSE) exec -T postgres pg_isready -U "$${POSTGRES_USER:-inventory}" -d "$${POSTGRES_DB:-inventory_read}" >/dev/null 2>&1 \
			&& { echo "postgres is ready"; exit 0; }; \
		sleep 2; \
	done; echo "postgres did not become ready"; exit 1

## Creates the read-only role used by the API's queries. Run after `make migrate`.
pg-grants:
	@test -n "$$POSTGRES_READER_PASSWORD" || { echo "set POSTGRES_READER_PASSWORD in .env"; exit 1; }
	@sed -e "s/{{database}}/$${POSTGRES_DB:-inventory_read}/g" -e "s/{{reader_user}}/$${POSTGRES_READER_USER:-inventory_reader}/g" \
		-e "s/{{reader_password}}/$$POSTGRES_READER_PASSWORD/g" deploy/postgres/grants.sql \
		| $(COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -q -U "$${POSTGRES_USER:-inventory}" -d "$${POSTGRES_DB:-inventory_read}"

## Creates the restricted application user and grants it. Run after the schema is migrated.
mysql-grants:
	@test -n "$$MYSQL_APP_PASSWORD" || { echo "set MYSQL_APP_PASSWORD in .env"; exit 1; }
	@sed -e "s/{{database}}/$(MYSQL_DATABASE)/g" -e "s/{{app_user}}/$(MYSQL_APP_USER)/g" \
		-e "s/{{app_password}}/$$MYSQL_APP_PASSWORD/g" deploy/mysql/grants.sql \
		| $(COMPOSE) exec -T mysql mysql -uroot -p"$$MYSQL_ROOT_PASSWORD"
