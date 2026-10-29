package events

import "keyloop-coding-challenge/internal/domain"

// VehicleSoldEvent ends the lifecycle. ClosedActionID names the action the sale
// closed, or is empty when no action was active.
type VehicleSoldEvent struct {
	SoldOn         domain.Date `json:"sold_on"`
	ClosedActionID string      `json:"closed_action_id,omitempty"`
}
