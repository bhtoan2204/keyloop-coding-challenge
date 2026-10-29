# Local values (passwords, ports, DSNs) come from .env; see .env.example.
-include .env
export

COMPOSE ?= docker compose
MYSQL_DATABASE ?= inventory
MYSQL_APP_USER ?= inventory_app

.PHONY: fmt-check vet test test-race test-integration check build migrate run-api sync-projections rebuild-projections up mysql-up mysql-wait mysql-down mysql-grants pg-up pg-wait pg-grants

fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt needed"; exit 1; }

vet:
	go vet ./...
	go vet -tags integration ./...

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

## Runs the MySQL- and PostgreSQL-backed tests. Needs `make up`, MYSQL_TEST_DSN and PG_TEST_DSN.
## The tests fail (never skip) when a DSN is missing.
test-integration:
	go test -tags integration -count=1 -v ./...

check: fmt-check vet test test-race

build:
	go build -o bin/inventory ./cmd

## Applies migrations with the admin account (MYSQL_MIGRATE_DSN or root + MYSQL_ROOT_PASSWORD).
migrate:
	go run ./cmd migrate

## Serves the API: commands as the restricted MySQL app user, queries as the PostgreSQL reader.
run-api:
	go run ./cmd api

## Phase 4 read-model refresh (NOT real time): applies events committed since the last run,
## read from one consistent MySQL snapshot. Safe to repeat; one run at a time.
sync-projections:
	go run ./cmd sync-projections

## Rebuilds the whole read model from the MySQL history in one transaction.
rebuild-projections:
	go run ./cmd rebuild-projections

up: mysql-up pg-up

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
