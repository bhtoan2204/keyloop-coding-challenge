-- Read-only role for the API's query side. Apply AFTER `migrate` (tables must exist).
-- Projections are written by the schema owner (sync-projections / rebuild-projections).
-- Placeholders, substituted by `make pg-grants` and the integration test:
--   {{database}}  {{reader_user}}  {{reader_password}}

DO $$
BEGIN
	IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '{{reader_user}}') THEN
		CREATE ROLE {{reader_user}} LOGIN PASSWORD '{{reader_password}}';
	END IF;
	-- Re-applied on every run, so a changed password in .env takes effect.
	ALTER ROLE {{reader_user}} LOGIN PASSWORD '{{reader_password}}';
END
$$;

GRANT CONNECT ON DATABASE {{database}} TO {{reader_user}};
GRANT USAGE ON SCHEMA public TO {{reader_user}};
-- No INSERT, UPDATE, DELETE or TRUNCATE anywhere.
GRANT SELECT ON dealership, inventory_vehicle, vehicle_action_history, projection_run TO {{reader_user}};
