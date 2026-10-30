package models

import "time"

// ProjectionAggregate records the last event applied per aggregate. It commits in the
// same transaction as the projected rows.
type ProjectionAggregate struct {
	AggregateID string    `gorm:"column:aggregate_id;primaryKey"`
	Version     int       `gorm:"column:version"`
	LastEventID int64     `gorm:"column:last_event_id"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (ProjectionAggregate) TableName() string { return "read_model.projection_aggregate" }
