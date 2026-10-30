package models

import "time"

// VehicleActionHistory is a row of read_model.vehicle_action_history; (vehicle_id,
// version) is the key, event_id the event-store id of the event that recorded it.
type VehicleActionHistory struct {
	VehicleID     string    `gorm:"column:vehicle_id;primaryKey"`
	Version       int       `gorm:"column:version;primaryKey"`
	EventID       int64     `gorm:"column:event_id"`
	ActionID      string    `gorm:"column:action_id"`
	Change        string    `gorm:"column:change"`
	ActionType    string    `gorm:"column:action_type"`
	Note          string    `gorm:"column:note"`
	Reason        string    `gorm:"column:reason"`
	ActorID       string    `gorm:"column:actor_id"`
	CorrelationID string    `gorm:"column:correlation_id"`
	OccurredAt    time.Time `gorm:"column:occurred_at"`
}

func (VehicleActionHistory) TableName() string { return "read_model.vehicle_action_history" }
