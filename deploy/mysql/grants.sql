-- Restricted application user for the event store. Apply AFTER migrations: MySQL only
-- grants table privileges on tables that exist. Migrations run with an admin account.
-- Placeholders, substituted by `make mysql-grants` and the integration test:
--   {{database}}  {{app_user}}  {{app_password}}

CREATE USER IF NOT EXISTS '{{app_user}}'@'%' IDENTIFIED BY '{{app_password}}';

-- Aggregate registry and optimistic-concurrency CAS. INSERT ... ON DUPLICATE KEY
-- UPDATE needs INSERT and UPDATE.
GRANT SELECT, INSERT, UPDATE ON `{{database}}`.`es_aggregate` TO '{{app_user}}'@'%';

-- Append-only event history: no UPDATE, no DELETE.
GRANT SELECT, INSERT ON `{{database}}`.`es_event` TO '{{app_user}}'@'%';

GRANT SELECT, INSERT ON `{{database}}`.`es_aggregate_snapshot` TO '{{app_user}}'@'%';
GRANT SELECT ON `{{database}}`.`es_schema_migrations` TO '{{app_user}}'@'%';

-- Dealership reference data (read-only for the application).
GRANT SELECT ON `{{database}}`.`dealership` TO '{{app_user}}'@'%';
