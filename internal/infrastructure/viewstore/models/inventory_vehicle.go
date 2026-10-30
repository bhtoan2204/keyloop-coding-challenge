package models

import (
	"database/sql"
	"time"
)

// InventoryVehicle is a row of read_model.inventory_vehicle. The action columns are
// NULL while the vehicle has no active action.
type InventoryVehicle struct {
	VehicleID        string         `gorm:"column:vehicle_id;primaryKey"`
	DealershipID     string         `gorm:"column:dealership_id"`
	VIN              string         `gorm:"column:vin"`
	Make             string         `gorm:"column:make"`
	Model            string         `gorm:"column:model"`
	ModelYear        int            `gorm:"column:model_year"`
	StockedOn        Date           `gorm:"column:stocked_on"`
	Status           string         `gorm:"column:status"`
	SoldOn           NullDate       `gorm:"column:sold_on"`
	ActionID         sql.NullString `gorm:"column:action_id"`
	ActionType       sql.NullString `gorm:"column:action_type"`
	ActionNote       sql.NullString `gorm:"column:action_note"`
	ActionProposedAt sql.NullTime   `gorm:"column:action_proposed_at"`
	ActionUpdatedAt  sql.NullTime   `gorm:"column:action_updated_at"`
	ActionUpdatedBy  sql.NullString `gorm:"column:action_updated_by"`
	Version          int            `gorm:"column:version"`
	LastEventID      int64          `gorm:"column:last_event_id"`
	// Set from the event time by the projector; GORM must not stamp it with now().
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (InventoryVehicle) TableName() string { return "read_model.inventory_vehicle" }
