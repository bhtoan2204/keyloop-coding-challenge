package events

import "keyloop-coding-challenge/internal/domain"

// VehicleStockedEvent starts a vehicle's inventory lifecycle. The vehicle ID is the
// aggregate ID of the stream and is not repeated in the payload.
type VehicleStockedEvent struct {
	DealershipID string      `json:"dealership_id"`
	VIN          string      `json:"vin"`
	Make         string      `json:"make"`
	Model        string      `json:"model"`
	ModelYear    int         `json:"model_year"`
	StockedOn    domain.Date `json:"stocked_on"`
}
