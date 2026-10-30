-- Rebuild role: `rebuild-projections`. Apply AFTER `migrate`. It reads the event
-- history and rewrites the read model (TRUNCATE + replay, logged in projection_run);
-- it cannot write events, change the schema or touch the reference data. Not a
-- superuser. Migrations stay with the admin account.
-- Placeholders, substituted by `make pg-grants`, compose and the integration tests:
--   {{database}}  {{rebuild_user}}  {{rebuild_password}}

DO $$
BEGIN
	IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '{{rebuild_user}}') THEN
		CREATE ROLE {{rebuild_user}} LOGIN PASSWORD '{{rebuild_password}}';
	END IF;
	-- Re-applied on every run, so a changed password in .env takes effect.
	ALTER ROLE {{rebuild_user}} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '{{rebuild_password}}';
END
$$;

GRANT CONNECT ON DATABASE {{database}} TO {{rebuild_user}};
GRANT USAGE ON SCHEMA event_store, read_model TO {{rebuild_user}};
GRANT SELECT ON event_store.es_event TO {{rebuild_user}};
GRANT SELECT, INSERT, UPDATE, TRUNCATE ON read_model.inventory_vehicle, read_model.vehicle_action_history, read_model.projection_aggregate TO {{rebuild_user}};
GRANT SELECT, INSERT ON read_model.projection_run TO {{rebuild_user}};
GRANT USAGE ON SEQUENCE read_model.projection_run_run_id_seq TO {{rebuild_user}};
