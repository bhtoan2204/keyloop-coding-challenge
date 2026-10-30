-- Read-only role for the API's query side. Apply AFTER `migrate` (tables must exist).
-- Placeholders, substituted by `make pg-grants`, compose and the integration tests:
--   {{database}}  {{reader_user}}  {{reader_password}}

DO $$
BEGIN
	IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '{{reader_user}}') THEN
		CREATE ROLE {{reader_user}} LOGIN PASSWORD '{{reader_password}}';
	END IF;
	-- Re-applied on every run, so a changed password in .env takes effect.
	ALTER ROLE {{reader_user}} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '{{reader_password}}';
END
$$;

GRANT CONNECT ON DATABASE {{database}} TO {{reader_user}};
GRANT USAGE ON SCHEMA read_model, event_store TO {{reader_user}};
-- No INSERT, UPDATE, DELETE or TRUNCATE anywhere, and no access to the events.
GRANT SELECT ON read_model.inventory_vehicle, read_model.vehicle_action_history, read_model.projection_run TO {{reader_user}};
GRANT SELECT ON event_store.dealership TO {{reader_user}};
