package commands

// vehicleResponse is the committed write-side state after a command. age_days and
// is_aging are derived at business_date and never stored.
type vehicleResponse struct {
	VehicleID     string          `json:"vehicle_id"`
	DealershipID  string          `json:"dealership_id"`
	VIN           string          `json:"vin"`
	Make          string          `json:"make"`
	Model         string          `json:"model"`
	ModelYear     int             `json:"model_year"`
	StockedOn     string          `json:"stocked_on"`
	Status        string          `json:"status"`
	SoldOn        *string         `json:"sold_on"`
	AgeDays       int             `json:"age_days"`
	IsAging       bool            `json:"is_aging"`
	CurrentAction *actionResponse `json:"current_action"`
	Version       int             `json:"version"`
	BusinessDate  string          `json:"business_date"`
}

type actionResponse struct {
	ActionID   string `json:"action_id"`
	ActionType string `json:"action_type"`
	Note       string `json:"note"`
}
