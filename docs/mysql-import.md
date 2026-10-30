# Importing the MySQL event history (one-off)

Until Phase 7 the event store lived in MySQL 8.4 (Compose service `mysql`, volume `mysql-data`). Phase 7 moved it into the `event_store` schema of the PostgreSQL database. `migrate` never touches MySQL, so an existing MySQL history is **not** carried over automatically. This page describes the explicit, one-off import: `deploy/import/mysql-history.sh`.

Use it only if you have history in the old MySQL event store that must be kept. A fresh installation does not need it.

## What the script does

1. **Checks the target first**, before reading anything from MySQL: `event_store` must be migrated (`event_store.schema_migrations` exists) and **empty** (no rows in `es_aggregate`, `es_event` or `es_aggregate_snapshot`). Otherwise it refuses.
2. **Exports** `es_aggregate`, `es_event`, `es_aggregate_snapshot` and `dealership` from MySQL to TSV files in `EXPORT_DIR`. Every text column is hex-encoded, so tabs, newlines and backslashes in payloads cannot break the files. It only runs `SELECT`s: it never writes to MySQL.
3. **Loads everything in ONE `psql` transaction**:
   - `LOCK TABLE … IN EXCLUSIVE MODE` on the three event-store tables (blocks writers until commit), then re-checks that they are still empty;
   - keeps event ids, aggregate ids, versions, payloads, metadata, `schema_version` and `created_at` exactly as they were;
   - fills an empty legacy `aggregate_type` on an event from its aggregate (rows written before the old MySQL v2 migration);
   - inserts dealerships with `ON CONFLICT DO NOTHING`, so the seeded `d-001`/`d-002` are kept and only extra dealerships are added;
   - **verifies** before committing: row counts equal the export, every stream is contiguous `1..version` and ends at its aggregate's version, every event's `aggregate_type` matches its aggregate, and no snapshot is ahead of its stream. Any failure aborts the whole import (nothing is written);
   - moves the `es_event` identity past the highest imported id (`setval(max(id) + 1)`), so new events continue after the imported ones.
4. Prints the next step: rebuild the read model.

## Procedure

1. **Stop every writer of the MySQL event store** (the pre-Phase-7 API). The tables are exported one after the other; the verification rejects an inconsistent export, but it cannot recover events written during the export.
2. **Start PostgreSQL and migrate** (current stack): `make up && make migrate` (or `make stack-up`). Do not send commands to the new API before the import: the target must be empty.
3. **Start the old MySQL** with its existing volume. The current `docker-compose.yaml` no longer declares the `mysql` service or the `mysql-data` volume, but the volume is not deleted; start a MySQL 8.4 container on it, or use any MySQL server that holds the history.
4. **Run the import:**

   ```bash
   MYSQL="docker exec -i -e MYSQL_PWD <mysql-container> mysql -uroot inventory" \
   PSQL="docker exec -i <postgres-container> psql -U inventory -d inventory_read" \
   EXPORT_DIR=./mysql-export \
   sh deploy/import/mysql-history.sh
   ```

   - `MYSQL`: a `mysql` client command already pointing at the source database (default `mysql`). It needs only `SELECT` on the four tables: the old MySQL `root` account or any SELECT-capable user. With `docker exec -e MYSQL_PWD`, set `MYSQL_PWD` in your shell first so the password does not appear on the command line.
   - `PSQL`: a `psql` command connected to the target database **as the admin role** (`POSTGRES_USER`), e.g. `psql postgres://inventory:SECRET@127.0.0.1:5432/inventory_read` (default `psql`).
   - `EXPORT_DIR`: where the TSV files go (default: a new temporary directory).
   - Use `podman` instead of `docker` on a Podman host.
5. **Rebuild the read model** from the imported history: `make rebuild-projections`, or in containers `docker compose --profile tools run --rm --no-deps rebuild` (needs `make pg-grants` / the `postgres-grants` job to have run).
6. **Delete `EXPORT_DIR`** when done. It holds the full event history (created with `umask 077`, but still plaintext).

## Credentials of the old stack

The import needs the old MySQL `root` password (or the password of another account with `SELECT` on the event-store tables). The earlier development stack's volumes still exist on the development machine, but the `.env` values that created them were not kept, so **the developer must supply those credentials**. Nothing in the repository holds them. Without them the MySQL history cannot be read, and the PostgreSQL event store starts empty.

## Guarantees and limits

- **All or nothing:** the load and its verification run in one transaction; a failed check or error leaves the PostgreSQL event store empty.
- **Never overwrites:** it refuses a non-empty `event_store`, both before exporting and again under the table lock.
- **Never writes to MySQL.**
- **Not automatic:** no command or container runs it; it must be invoked explicitly.
- **Read model:** the import does not touch `read_model`; run `rebuild-projections` afterwards. A read model upgraded in place from the Phase 4–6 layout (by `migrate`) may already hold rows for the same vehicles; the rebuild replaces it with exactly what the imported history projects.
- **Upgraded volume without an import:** if `migrate` adopted a Phase 4–6 read model but the MySQL history was never imported, those vehicles are visible to queries but have no events. Commands on them return 404. Stocking a vehicle with one of those ids fails with a version-gap error (500), because the read model is ahead of the event store. Import the history and rebuild, or rebuild on its own to drop the orphaned rows.
- **Dealerships:** a dealership that exists on both sides must have the same time zone, or the import aborts. Business dates depend on it.
- **Consistency of the export:** the four tables are exported one after another, not in one MySQL snapshot. Stop every MySQL writer first; the verification rejects an export whose versions do not line up.
- **Passwords:** pass them in `MYSQL_PWD` / `PGPASSWORD` (or option files), not on the command line where `ps` shows them. `EXPORT_DIR` must be new or empty.
- **Verification status:** exercised manually during Phase 7 against a throwaway MySQL 8.4 populated by the pre-Phase-7 binary; see `docs/ai-log.md`. There is no automated test for the script.
