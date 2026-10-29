package events

import "keyloop-coding-challenge/internal/domain"

type VehicleActionProposedEvent struct {
	ActionID   string            `json:"action_id"`
	ActionType domain.ActionType `json:"action_type"`
	Note       string            `json:"note"`
}
