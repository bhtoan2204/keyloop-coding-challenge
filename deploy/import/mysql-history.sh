#!/bin/sh
# One-off, explicitly invoked import of the event history of the earlier MySQL event
# store into the PostgreSQL event_store schema (docs/mysql-import.md).
#
# It never writes to MySQL and never overwrites PostgreSQL history: it refuses unless
# event_store is migrated and EMPTY, and loads everything in ONE transaction that also
# verifies the result, so it either imports the whole history or nothing.
# Event ids, aggregate ids, versions, payloads, metadata, schema versions and created_at
# are kept as they are; the es_event identity is then moved past the highest id.
# Afterwards rebuild the read model: `inventory rebuild-projections`.
#
# Stop every writer of the MySQL event store first (the earlier stack's API): the
# tables are exported one after the other, and the verification below rejects an
# export that is not consistent.
#
# Environment:
#   MYSQL   MySQL client command, already pointing at the source database with a
#           SELECT-capable account, e.g. "mysql -h 127.0.0.1 -u root inventory" with the
#           password in MYSQL_PWD (not -p…, which shows in ps)       (default: mysql)
#   PSQL    psql command connected to the target database as the admin role, e.g.
#           "psql -h 127.0.0.1 -U inventory -d inventory_read" with PGPASSWORD set
#                                                                    (default: psql)
#   EXPORT_DIR  a new or empty directory for the exported TSV files (default: a new
#           temp dir); kept afterwards for inspection: it contains the full history
set -euf

MYSQL=${MYSQL:-mysql}
PSQL=${PSQL:-psql}
EXPORT_DIR=${EXPORT_DIR:-$(mktemp -d)}
umask 077
mkdir -p "$EXPORT_DIR"
# The files have fixed names: refuse a directory with anything in it (stale exports,
# planted symlinks).
if [ -n "$(ls -A "$EXPORT_DIR")" ]; then
	echo "EXPORT_DIR $EXPORT_DIR is not empty" >&2
	exit 1
fi

psql_value() {
	$PSQL -X -v ON_ERROR_STOP=1 -At -c "$1"
}

# --- target checks (before reading anything from MySQL) ----------------------------
migrated=$(psql_value "SELECT to_regclass('event_store.schema_migrations') IS NOT NULL")
if [ "$migrated" != "t" ]; then
	echo "target: event_store is not migrated; run \`inventory migrate\` first" >&2
	exit 1
fi
existing=$(psql_value "SELECT (SELECT count(*) FROM event_store.es_aggregate) + (SELECT count(*) FROM event_store.es_event) + (SELECT count(*) FROM event_store.es_aggregate_snapshot)")
if [ "$existing" != "0" ]; then
	echo "target: event_store already holds $existing rows; refusing to import into a populated event store" >&2
	exit 1
fi

# --- export: text columns hex-encoded, so tabs, newlines and backslashes in payloads
# cannot break the TSV; NULL metadata becomes COPY's \N ------------------------------
export_query() {
	$MYSQL --batch --raw --skip-column-names -e "$1" > "$EXPORT_DIR/$2"
	echo "exported $(wc -l < "$EXPORT_DIR/$2" | tr -d ' ') rows to $EXPORT_DIR/$2"
}
export_query "SELECT HEX(id), version, HEX(aggregate_type) FROM es_aggregate ORDER BY id" es_aggregate.tsv
export_query "SELECT id, HEX(aggregate_id), HEX(aggregate_type), HEX(event_type), version, schema_version,
	HEX(data), IFNULL(HEX(metadata), '\\\\N'), created_at FROM es_event ORDER BY id" es_event.tsv
export_query "SELECT HEX(aggregate_id), version, HEX(data) FROM es_aggregate_snapshot ORDER BY aggregate_id, version" es_aggregate_snapshot.tsv
export_query "SELECT HEX(dealership_id), HEX(name), HEX(timezone) FROM dealership ORDER BY dealership_id" dealership.tsv

# --- load and verify in one transaction ------------------------------------------------
{
	cat <<'SQL'
\set ON_ERROR_STOP 1
BEGIN;
-- Blocks writers (the API) until commit and re-checks emptiness under that lock.
LOCK TABLE event_store.es_aggregate, event_store.es_event, event_store.es_aggregate_snapshot IN EXCLUSIVE MODE;
DO $$ BEGIN
	IF EXISTS (SELECT FROM event_store.es_aggregate) OR EXISTS (SELECT FROM event_store.es_event)
		OR EXISTS (SELECT FROM event_store.es_aggregate_snapshot) THEN
		RAISE EXCEPTION 'event_store is not empty';
	END IF;
END $$;
CREATE TEMP TABLE src_aggregate (id TEXT, version BIGINT, aggregate_type TEXT) ON COMMIT DROP;
CREATE TEMP TABLE src_event (id BIGINT, aggregate_id TEXT, aggregate_type TEXT, event_type TEXT, version BIGINT,
	schema_version INT, data TEXT, metadata TEXT, created_at BIGINT) ON COMMIT DROP;
CREATE TEMP TABLE src_snapshot (aggregate_id TEXT, version BIGINT, data TEXT) ON COMMIT DROP;
CREATE TEMP TABLE src_dealership (dealership_id TEXT, name TEXT, timezone TEXT) ON COMMIT DROP;
SQL
	for pair in src_aggregate:es_aggregate src_event:es_event src_snapshot:es_aggregate_snapshot src_dealership:dealership; do
		echo "COPY ${pair%%:*} FROM STDIN;"
		cat "$EXPORT_DIR/${pair#*:}.tsv"
		printf '%s\n' '\.'
	done
	cat <<'SQL'
CREATE FUNCTION pg_temp.unhex(h TEXT) RETURNS TEXT LANGUAGE sql IMMUTABLE
	AS $$ SELECT convert_from(decode(h, 'hex'), 'UTF8') $$;

-- A dealership already present must have the same time zone: it decides business dates
-- (aging, stocked_on validation) of the imported vehicles.
DO $$
DECLARE
	bad TEXT;
BEGIN
	SELECT d.dealership_id INTO bad
	FROM src_dealership s JOIN event_store.dealership d ON d.dealership_id = pg_temp.unhex(s.dealership_id)
	WHERE d.timezone <> pg_temp.unhex(s.timezone) LIMIT 1;
	IF bad IS NOT NULL THEN
		RAISE EXCEPTION 'dealership % : time zone differs between MySQL and event_store.dealership', bad;
	END IF;
END $$;
INSERT INTO event_store.dealership (dealership_id, name, timezone)
SELECT pg_temp.unhex(dealership_id), pg_temp.unhex(name), pg_temp.unhex(timezone) FROM src_dealership
ON CONFLICT (dealership_id) DO NOTHING;

INSERT INTO event_store.es_aggregate (id, version, aggregate_type)
SELECT pg_temp.unhex(id), version, pg_temp.unhex(aggregate_type) FROM src_aggregate;

-- Rows written before the earlier v2 migration may have an empty aggregate_type that it
-- did not backfill: take the aggregate's type, as that migration intended.
INSERT INTO event_store.es_event
	(id, aggregate_id, aggregate_type, event_type, version, schema_version, data, metadata, created_at)
SELECT e.id, a.id, COALESCE(NULLIF(pg_temp.unhex(e.aggregate_type), ''), a.aggregate_type),
	pg_temp.unhex(e.event_type), e.version, e.schema_version, pg_temp.unhex(e.data),
	CASE WHEN e.metadata IS NULL THEN NULL ELSE pg_temp.unhex(e.metadata) END, e.created_at
FROM src_event e
LEFT JOIN event_store.es_aggregate a ON a.id = pg_temp.unhex(e.aggregate_id)
ORDER BY e.id;

INSERT INTO event_store.es_aggregate_snapshot (aggregate_id, version, data)
SELECT pg_temp.unhex(aggregate_id), version, pg_temp.unhex(data) FROM src_snapshot;

-- Verification: every source row arrived, every stream is contiguous from 1 and ends at
-- its aggregate's version, every event's type matches its aggregate, and no snapshot is
-- ahead of its stream. Any failure aborts the whole import.
DO $$
DECLARE
	bad TEXT;
BEGIN
	IF (SELECT count(*) FROM event_store.es_aggregate) <> (SELECT count(*) FROM src_aggregate)
		OR (SELECT count(*) FROM event_store.es_event) <> (SELECT count(*) FROM src_event)
		OR (SELECT count(*) FROM event_store.es_aggregate_snapshot) <> (SELECT count(*) FROM src_snapshot) THEN
		RAISE EXCEPTION 'row counts differ from the export';
	END IF;
	SELECT a.id INTO bad
	FROM event_store.es_aggregate a
	LEFT JOIN (SELECT aggregate_id, count(*) AS n, min(version) AS lo, max(version) AS hi
		FROM event_store.es_event GROUP BY aggregate_id) s ON s.aggregate_id = a.id
	WHERE COALESCE(s.n, 0) <> a.version OR (a.version > 0 AND (s.lo <> 1 OR s.hi <> a.version))
	LIMIT 1;
	IF bad IS NOT NULL THEN
		RAISE EXCEPTION 'aggregate % : its events are not contiguous 1..version (inconsistent export?)', bad;
	END IF;
	SELECT e.aggregate_id INTO bad
	FROM event_store.es_event e JOIN event_store.es_aggregate a ON a.id = e.aggregate_id
	WHERE e.aggregate_type <> a.aggregate_type LIMIT 1;
	IF bad IS NOT NULL THEN
		RAISE EXCEPTION 'aggregate % : event aggregate_type differs from its aggregate', bad;
	END IF;
	SELECT s.aggregate_id INTO bad
	FROM event_store.es_aggregate_snapshot s JOIN event_store.es_aggregate a ON a.id = s.aggregate_id
	WHERE s.version > a.version LIMIT 1;
	IF bad IS NOT NULL THEN
		RAISE EXCEPTION 'aggregate % : snapshot is ahead of its events', bad;
	END IF;
END $$;

-- New events continue after the imported ids.
DO $$ BEGIN
	PERFORM setval(pg_get_serial_sequence('event_store.es_event', 'id'),
		COALESCE((SELECT max(id) FROM event_store.es_event), 0) + 1, false);
END $$;

SELECT format('imported %s aggregates, %s events (ids %s..%s), %s snapshots',
	(SELECT count(*) FROM event_store.es_aggregate), (SELECT count(*) FROM event_store.es_event),
	(SELECT min(id) FROM event_store.es_event), (SELECT max(id) FROM event_store.es_event),
	(SELECT count(*) FROM event_store.es_aggregate_snapshot));
COMMIT;
SQL
} | $PSQL -X -q -At -f -

echo "done. Next: rebuild the read model (inventory rebuild-projections / docker compose run --rm rebuild)."
echo "The export in $EXPORT_DIR contains the full event history: delete it when no longer needed."
