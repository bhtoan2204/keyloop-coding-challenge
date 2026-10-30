package viewstore

import (
	"fmt"

	"gorm.io/gorm"
)

// legacyTables are the projected tables that Phases 4-6 created in the public schema.
var legacyTables = []string{
	"inventory_vehicle",
	"vehicle_action_history",
	"projection_aggregate",
	"projection_run",
}

// createOrAdoptReadModelTables creates the read_model tables, or, on a database that
// still has the earlier layout in public, moves those tables (with their rows, indexes
// and sequences) into read_model and re-points the dealership foreign key at
// event_store.dealership. Nothing is dropped: public.dealership and
// public.rm_schema_migrations stay as they are. It runs inside the migration
// transaction, so a failure (e.g. a projected dealership missing from
// event_store.dealership) leaves the database unchanged.
func createOrAdoptReadModelTables(tx *gorm.DB) error {
	var hasDealerships bool
	if err := tx.Raw(`SELECT to_regclass('event_store.dealership') IS NOT NULL`).Scan(&hasDealerships).Error; err != nil {
		return err
	}
	if !hasDealerships {
		return fmt.Errorf("event_store.dealership is missing: migrate the event store first")
	}
	legacy, err := hasLegacyLayout(tx)
	if err != nil {
		return err
	}
	if !legacy {
		if err := tx.Exec(readModelTablesDDL).Error; err != nil {
			return err
		}
		return tx.Exec(dealershipForeignKeyDDL).Error
	}
	for _, table := range legacyTables {
		if err := tx.Exec(fmt.Sprintf(`ALTER TABLE public.%s SET SCHEMA read_model`, table)).Error; err != nil {
			return fmt.Errorf("move public.%s: %w", table, err)
		}
	}
	if err := tx.Exec(`ALTER TABLE read_model.inventory_vehicle DROP CONSTRAINT IF EXISTS inventory_vehicle_dealership_id_fkey`).Error; err != nil {
		return err
	}
	return tx.Exec(dealershipForeignKeyDDL).Error
}

// hasLegacyLayout reports a complete earlier read model in public. A partial one is
// refused rather than guessed at.
func hasLegacyLayout(tx *gorm.DB) (bool, error) {
	found := 0
	for _, table := range legacyTables {
		var exists bool
		if err := tx.Raw(`SELECT to_regclass(?) IS NOT NULL`, "public."+table).Scan(&exists).Error; err != nil {
			return false, err
		}
		if exists {
			found++
		}
	}
	switch found {
	case 0:
		return false, nil
	case len(legacyTables):
		return true, nil
	}
	return false, fmt.Errorf("public has %d of the %d earlier read-model tables: refusing to guess, move or drop them by hand", found, len(legacyTables))
}
