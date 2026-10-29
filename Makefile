# Local values (passwords, ports, DSNs) come from .env; see .env.example.
-include .env
export

COMPOSE ?= docker compose
MYSQL_DATABASE ?= inventory
MYSQL_APP_USER ?= inventory_app

.PHONY: fmt-check vet test test-race test-integration check mysql-up mysql-wait mysql-down mysql-grants

fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt needed"; exit 1; }

vet:
	go vet ./...
	go vet -tags integration ./...

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

## Runs the MySQL-backed tests. Needs a running MySQL (make mysql-up) and MYSQL_TEST_DSN.
## The tests fail (never skip) when MYSQL_TEST_DSN is missing.
test-integration:
	go test -tags integration -count=1 -v ./internal/infrastructure/eventstore/...

check: fmt-check vet test test-race

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

## Creates the restricted application user and grants it. Run after the schema is migrated.
mysql-grants:
	@test -n "$$MYSQL_APP_PASSWORD" || { echo "set MYSQL_APP_PASSWORD in .env"; exit 1; }
	@sed -e "s/{{database}}/$(MYSQL_DATABASE)/g" -e "s/{{app_user}}/$(MYSQL_APP_USER)/g" \
		-e "s/{{app_password}}/$$MYSQL_APP_PASSWORD/g" deploy/mysql/grants.sql \
		| $(COMPOSE) exec -T mysql mysql -uroot -p"$$MYSQL_ROOT_PASSWORD"
