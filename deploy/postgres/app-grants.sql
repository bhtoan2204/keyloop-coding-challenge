-- Application writer: the API's command side. Apply AFTER `migrate`. One transaction
-- appends events and updates the read model, so it needs both schemas, but the event
-- history stays append-only: INSERT and SELECT on es_event, never UPDATE or DELETE.
-- No TRUNCATE, no DDL, not a superuser.
-- Placeholders, substituted by `make pg-grants`, compose and the integration tests:
--   {{database}}  {{app_user}}  {{app_password}}

DO $$
BEGIN
	IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '{{app_user}}') THEN
		CREATE ROLE {{app_user}} LOGIN PASSWORD '{{app_password}}';
	END IF;
	-- Re-applied on every run, so a changed password in .env takes effect.
	ALTER ROLE {{app_user}} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '{{app_password}}';
END
$$;

GRANT CONNECT ON DATABASE {{database}} TO {{app_user}};
GRANT USAGE ON SCHEMA event_store, read_model TO {{app_user}};

GRANT SELECT, INSERT, UPDATE ON event_store.es_aggregate TO {{app_user}};
GRANT SELECT, INSERT ON event_store.es_event TO {{app_user}};
GRANT USAGE ON SEQUENCE event_store.es_event_id_seq TO {{app_user}};
GRANT SELECT, INSERT ON event_store.es_aggregate_snapshot TO {{app_user}};
GRANT SELECT ON event_store.dealership TO {{app_user}};

GRANT SELECT, INSERT, UPDATE ON read_model.inventory_vehicle, read_model.projection_aggregate TO {{app_user}};
GRANT SELECT, INSERT ON read_model.vehicle_action_history TO {{app_user}};
