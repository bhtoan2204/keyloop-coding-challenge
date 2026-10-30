package migrations

// readModelTablesDDL creates the projected tables. There is deliberately no is_aging or
// days_in_stock column: both change daily and are computed at query time from
// stocked_on and the dealership business date. Dealership reference data is not
// copied: the foreign key and the queries use event_store.dealership.
const readModelTablesDDL = `
CREATE TABLE read_model.inventory_vehicle (
	vehicle_id         TEXT PRIMARY KEY,
	dealership_id      TEXT     NOT NULL,
	vin                TEXT     NOT NULL,
	make               TEXT     NOT NULL,
	model              TEXT     NOT NULL,
	model_year         INT      NOT NULL,
	stocked_on         DATE     NOT NULL,
	status             TEXT     NOT NULL CHECK (status IN ('IN_STOCK', 'SOLD')),
	sold_on            DATE,
	action_id          TEXT,
	action_type        TEXT,
	action_note        TEXT,
	action_proposed_at TIMESTAMPTZ,
	action_updated_at  TIMESTAMPTZ,
	action_updated_by  TEXT,
	version            BIGINT      NOT NULL CHECK (version >= 1),
	last_event_id      BIGINT      NOT NULL,
	updated_at         TIMESTAMPTZ NOT NULL,
	CHECK ((status = 'SOLD') = (sold_on IS NOT NULL))
);
-- Default list, age filters, aging-only and both age sorts (backward scan for age_asc).
CREATE INDEX ix_vehicle_dealer_status_stocked ON read_model.inventory_vehicle (dealership_id, status, stocked_on, vehicle_id);
-- make / make+model filters, ordered by age within them.
CREATE INDEX ix_vehicle_dealer_make_model ON read_model.inventory_vehicle (dealership_id, status, lower(make), lower(model), stocked_on, vehicle_id);

CREATE TABLE read_model.vehicle_action_history (
	vehicle_id     TEXT   NOT NULL REFERENCES read_model.inventory_vehicle ON DELETE CASCADE,
	version        BIGINT NOT NULL,
	event_id       BIGINT NOT NULL,
	action_id      TEXT   NOT NULL,
	change         TEXT   NOT NULL CHECK (change IN ('PROPOSED', 'REVISED', 'CANCELLED', 'CLOSED_BY_SALE')),
	action_type    TEXT   NOT NULL,
	note           TEXT   NOT NULL DEFAULT '',
	reason         TEXT   NOT NULL DEFAULT '',
	actor_id       TEXT   NOT NULL DEFAULT '',
	correlation_id TEXT   NOT NULL DEFAULT '',
	occurred_at    TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (vehicle_id, version)
);

-- Projection bookkeeping: last aggregate version applied, committed in the same
-- transaction as the projected state (and, for commands, as the events themselves).
CREATE TABLE read_model.projection_aggregate (
	aggregate_id  TEXT PRIMARY KEY,
	version       BIGINT NOT NULL CHECK (version >= 1),
	last_event_id BIGINT NOT NULL,
	updated_at    TIMESTAMPTZ NOT NULL
);

-- One row per completed rebuild.
CREATE TABLE read_model.projection_run (
	run_id         BIGSERIAL PRIMARY KEY,
	kind           TEXT NOT NULL CHECK (kind IN ('sync', 'rebuild')),
	started_at     TIMESTAMPTZ NOT NULL,
	finished_at    TIMESTAMPTZ NOT NULL,
	events_read    BIGINT NOT NULL,
	events_applied BIGINT NOT NULL
);
`

const dealershipForeignKeyDDL = `
ALTER TABLE read_model.inventory_vehicle
	ADD CONSTRAINT inventory_vehicle_dealership_fk
	FOREIGN KEY (dealership_id) REFERENCES event_store.dealership (dealership_id)`
