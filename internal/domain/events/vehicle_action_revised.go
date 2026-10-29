package events

import "keyloop-coding-challenge/internal/domain"

// VehicleActionRevisedEvent carries the full new values of the active action.
type VehicleActionRevisedEvent struct {
	ActionID   string            `json:"action_id"`
	ActionType domain.ActionType `json:"action_type"`
	Note       string            `json:"note"`
}
