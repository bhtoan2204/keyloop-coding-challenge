-- MySQL account of the Debezium connector (deploy/debezium/es-event-connector.json).
-- Placeholders, substituted by `make debezium-grants` and the pipeline tests:
--   {{database}}  {{debezium_user}}  {{debezium_password}}

CREATE USER IF NOT EXISTS '{{debezium_user}}'@'%' IDENTIFIED BY '{{debezium_password}}';
-- Re-applied on every run, so a changed password in .env takes effect.
ALTER USER '{{debezium_user}}'@'%' IDENTIFIED BY '{{debezium_password}}';

-- Read the binlog (REPLICATION SLAVE, REPLICATION CLIENT) and take the brief global
-- read lock that fixes the snapshot's binlog position (RELOAD; snapshot.locking.mode
-- = minimal). These are global privileges in MySQL.
GRANT RELOAD, REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO '{{debezium_user}}'@'%';

-- The snapshot reads es_event only; no other table, no write privilege.
GRANT SELECT ON `{{database}}`.`es_event` TO '{{debezium_user}}'@'%';
