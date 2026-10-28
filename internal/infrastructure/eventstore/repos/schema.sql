-- MySQL schema for the event store.
-- repos.Migrate applies this DDL on MySQL and the SQLite equivalent in tests.

CREATE TABLE IF NOT EXISTS es_aggregate (
    id VARCHAR(255) NOT NULL PRIMARY KEY,
    version BIGINT NOT NULL,
    aggregate_type VARCHAR(255) NOT NULL
);

CREATE TABLE IF NOT EXISTS es_event (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    aggregate_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(255) NOT NULL,
    version BIGINT NOT NULL,
    data TEXT NOT NULL,
    metadata TEXT NULL,
    created_at BIGINT NOT NULL,
    UNIQUE KEY uq_es_event_aggregate_version (aggregate_id, version)
);

CREATE TABLE IF NOT EXISTS es_aggregate_snapshot (
    aggregate_id VARCHAR(255) NOT NULL,
    version BIGINT NOT NULL,
    data TEXT NOT NULL,
    PRIMARY KEY (aggregate_id, version)
);
