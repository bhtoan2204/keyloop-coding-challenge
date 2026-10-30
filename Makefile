# Local values (passwords, ports, DSNs) come from .env; see .env.example.
-include .env
export

COMPOSE ?= docker compose
# Make-level defaults: a shell-style ${VAR:-default} inside an exported variable would be
# expanded by make (to nothing) in sub-makes such as `$(MAKE) pg-wait`.
API_PORT ?= 8080
POSTGRES_DB ?= inventory_read
POSTGRES_USER ?= inventory
POSTGRES_APP_USER ?= inventory_app
POSTGRES_READER_USER ?= inventory_reader
POSTGRES_REBUILD_USER ?= inventory_rebuild

.PHONY: fmt-check vet test test-race test-integration e2e stack-up stack-wait stack-down stack-logs check build migrate run-api rebuild-projections up pg-up pg-wait pg-grants

fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt needed"; exit 1; }

vet:
	go vet ./...
	go vet -tags integration ./...
	go vet -tags e2e ./...

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

## Runs the PostgreSQL-backed tests. Needs a PostgreSQL 17 server (`make up`) and
## PG_TEST_DSN (admin, see .env.example). The tests fail (never skip) without it.
test-integration:
	go test -tags integration -count=1 -v ./...

## Business flow against a running system (docker compose stack or run-api).
## E2E_BASE_URL defaults to http://127.0.0.1:8080.
e2e:
	go test -tags e2e -count=1 -v ./internal/e2e/...

check: fmt-check vet test test-race

## The whole system in containers: PostgreSQL, migrations, roles, API (127.0.0.1:8080).
## Runs the init jobs one by one, so it works with Docker Compose and podman-compose
## alike (podman cannot start a container whose depends_on target has exited; with
## Docker Compose v2 a plain `docker compose up -d --build` does the same).
stack-up:
	$(COMPOSE) build migrate
	$(COMPOSE) up -d postgres
	$(MAKE) pg-wait
	$(COMPOSE) run --rm --no-deps migrate
	$(COMPOSE) run --rm --no-deps postgres-grants
	$(COMPOSE) up -d --no-deps api
	$(MAKE) stack-wait

## Waits until the API reports ready.
stack-wait:
	@for i in $$(seq 1 90); do \
		curl -fsS -m 3 http://127.0.0.1:$(API_PORT)/readyz >/dev/null 2>&1 \
			&& { echo "api is ready"; exit 0; }; \
		sleep 2; \
	done; echo "api not ready; see make stack-logs"; exit 1

## Stops and removes the containers; data volumes are kept (add -v by hand to wipe them).
stack-down:
	$(COMPOSE) down

stack-logs:
	$(COMPOSE) logs -f api

build:
	go build -o bin/inventory ./cmd

## Applies the event_store and read_model migrations as the admin role (PG_DSN or
## POSTGRES_USER + POSTGRES_PASSWORD).
migrate:
	go run ./cmd migrate

## Serves the API: commands as the application writer, queries as the read-only role.
run-api:
	go run ./cmd api

## Rebuilds the whole read model from event_store in one transaction (recovery).
## Refused while another rebuild runs; commands wait for it to finish.
rebuild-projections:
	go run ./cmd rebuild-projections

up: pg-up

pg-up:
	$(COMPOSE) up -d postgres
	$(MAKE) pg-wait

pg-wait:
	@for i in $$(seq 1 60); do \
		$(COMPOSE) exec -T postgres pg_isready -U "$(POSTGRES_USER)" -d "$(POSTGRES_DB)" >/dev/null 2>&1 \
			&& { echo "postgres is ready"; exit 0; }; \
		sleep 2; \
	done; echo "postgres did not become ready"; exit 1

## Creates the restricted roles (deploy/postgres): application writer, read-only role
## and rebuild role. Run after `make migrate`. Re-running updates their passwords.
pg-grants:
	@for n in '$(POSTGRES_DB)' '$(POSTGRES_APP_USER)' '$(POSTGRES_READER_USER)' '$(POSTGRES_REBUILD_USER)'; do \
		case "$$n" in ""|[!a-z_]*|*[!a-z0-9_]*) echo "POSTGRES_DB and the role names must be lower-case identifiers"; exit 1;; esac; \
	done
	@for p in "$$POSTGRES_APP_PASSWORD" "$$POSTGRES_READER_PASSWORD" "$$POSTGRES_REBUILD_PASSWORD"; do \
		case "$$p" in ""|*[!A-Za-z0-9]*) echo "set POSTGRES_APP/READER/REBUILD_PASSWORD (letters and digits) in .env"; exit 1;; esac; \
	done
	@sed -e "s/{{database}}/$(POSTGRES_DB)/g" -e "s/{{app_user}}/$(POSTGRES_APP_USER)/g" \
		-e "s/{{app_password}}/$$POSTGRES_APP_PASSWORD/g" deploy/postgres/app-grants.sql \
		| $(COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -q -U "$(POSTGRES_USER)" -d "$(POSTGRES_DB)"
	@sed -e "s/{{database}}/$(POSTGRES_DB)/g" -e "s/{{reader_user}}/$(POSTGRES_READER_USER)/g" \
		-e "s/{{reader_password}}/$$POSTGRES_READER_PASSWORD/g" deploy/postgres/grants.sql \
		| $(COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -q -U "$(POSTGRES_USER)" -d "$(POSTGRES_DB)"
	@sed -e "s/{{database}}/$(POSTGRES_DB)/g" -e "s/{{rebuild_user}}/$(POSTGRES_REBUILD_USER)/g" \
		-e "s/{{rebuild_password}}/$$POSTGRES_REBUILD_PASSWORD/g" deploy/postgres/rebuild-grants.sql \
		| $(COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -q -U "$(POSTGRES_USER)" -d "$(POSTGRES_DB)"
