package projection

import (
	"time"

	"keyloop-coding-challenge/internal/domain"
)

// ProjectedVehicle is the dashboard state of one vehicle. Zero dates and times mean
// "none" (no sale, no active action).
type ProjectedVehicle struct {
	VehicleID        string
	DealershipID     string
	VIN              string
	Make             string
	Model            string
	ModelYear        int
	StockedOn        domain.Date
	Status           domain.InventoryStatus
	SoldOn           domain.Date
	ActionID         string
	ActionType       domain.ActionType
	ActionNote       string
	ActionProposedAt time.Time
	ActionUpdatedAt  time.Time
	ActionUpdatedBy  string
	Version          int
	LastEventID      int64
	UpdatedAt        time.Time
}

// ActionHistoryEntry is one change of a vehicle's proposed action, with the id of the
// event that recorded it.
type ActionHistoryEntry struct {
	VehicleID     string
	Version       int
	EventID       int64
	ActionID      string
	Change        string
	ActionType    domain.ActionType
	Note          string
	Reason        string
	ActorID       string
	CorrelationID string
	OccurredAt    time.Time
}
