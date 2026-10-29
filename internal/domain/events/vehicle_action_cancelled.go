package events

type VehicleActionCancelledEvent struct {
	ActionID string `json:"action_id"`
	Reason   string `json:"reason"`
}
