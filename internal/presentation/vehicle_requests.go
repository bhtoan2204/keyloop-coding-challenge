package presentation

type stockVehicleRequest struct {
	VehicleID string `json:"vehicle_id"`
	VIN       string `json:"vin"`
	Make      string `json:"make"`
	Model     string `json:"model"`
	ModelYear int    `json:"model_year"`
	StockedOn string `json:"stocked_on"`
}

type actionRequest struct {
	ActionType string `json:"action_type"`
	Note       string `json:"note"`
}

type cancelActionRequest struct {
	Reason string `json:"reason"`
}

type sellVehicleRequest struct {
	SoldOn string `json:"sold_on"`
}
