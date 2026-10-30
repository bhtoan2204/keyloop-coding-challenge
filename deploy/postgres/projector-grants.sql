-- Projection writer: the role of the projector and of sync-projections /
-- rebuild-projections. Apply AFTER `migrate` (tables must exist). Not a superuser and
-- not the owner: it can change projected rows, empty them for a rebuild (TRUNCATE) and
-- log runs, but cannot change the schema or the reference data. Migrations stay with
-- the admin account.
-- Placeholders, substituted by `make pg-grants`, compose and the integration test:
--   {{database}}  {{projector_user}}  {{projector_password}}

DO $$
BEGIN
	IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '{{projector_user}}') THEN
		CREATE ROLE {{projector_user}} LOGIN PASSWORD '{{projector_password}}';
	END IF;
	-- Re-applied on every run, so a changed password in .env takes effect.
	ALTER ROLE {{projector_user}} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '{{projector_password}}';
END
$$;

GRANT CONNECT ON DATABASE {{database}} TO {{projector_user}};
GRANT USAGE ON SCHEMA public TO {{projector_user}};
GRANT SELECT, INSERT, UPDATE, TRUNCATE ON inventory_vehicle, vehicle_action_history, projection_aggregate TO {{projector_user}};
GRANT SELECT, INSERT ON projection_run TO {{projector_user}};
GRANT USAGE ON SEQUENCE projection_run_run_id_seq TO {{projector_user}};
