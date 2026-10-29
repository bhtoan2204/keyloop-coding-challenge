package queries

import "time"

type inventoryItem struct {
	VehicleID     string               `json:"vehicle_id"`
	DealershipID  string               `json:"dealership_id"`
	VIN           string               `json:"vin"`
	Make          string               `json:"make"`
	Model         string               `json:"model"`
	ModelYear     int                  `json:"model_year"`
	StockedOn     string               `json:"stocked_on"`
	Status        string               `json:"status"`
	SoldOn        *string              `json:"sold_on"`
	AgeDays       int                  `json:"age_days"`
	IsAging       bool                 `json:"is_aging"`
	CurrentAction *currentActionDetail `json:"current_action"`
	Version       int                  `json:"version"`
}

type currentActionDetail struct {
	ActionID   string    `json:"action_id"`
	ActionType string    `json:"action_type"`
	Note       string    `json:"note"`
	ProposedAt time.Time `json:"proposed_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	UpdatedBy  string    `json:"updated_by"`
}

type pageInfo struct {
	Limit      int     `json:"limit"`
	HasMore    bool    `json:"has_more"`
	NextCursor *string `json:"next_cursor"`
}

type inventoryListResponse struct {
	AsOf               string          `json:"as_of"`
	AgingThresholdDays int             `json:"aging_threshold_days"`
	Data               []inventoryItem `json:"data"`
	Page               pageInfo        `json:"page"`
}

type vehicleDetailResponse struct {
	inventoryItem
	AsOf string `json:"as_of"`
}

type historyItem struct {
	EventID       int64     `json:"event_id"`
	Version       int       `json:"version"`
	ActionID      string    `json:"action_id"`
	Change        string    `json:"change"`
	ActionType    string    `json:"action_type"`
	Note          string    `json:"note"`
	Reason        string    `json:"reason"`
	ActorID       string    `json:"actor_id"`
	CorrelationID string    `json:"correlation_id"`
	OccurredAt    time.Time `json:"occurred_at"`
}

type historyResponse struct {
	Data []historyItem `json:"data"`
	Page pageInfo      `json:"page"`
}

type summaryResponse struct {
	AsOf               string  `json:"as_of"`
	AgingThresholdDays int     `json:"aging_threshold_days"`
	InStock            int     `json:"in_stock"`
	Aging              int     `json:"aging"`
	AgingWithoutAction int     `json:"aging_without_action"`
	OldestStockedOn    *string `json:"oldest_stocked_on"`
}
