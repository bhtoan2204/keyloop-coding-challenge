package aggregates

import (
	"encoding/json"

	"keyloop-coding-challenge/internal/domain"
)

// vehicleInventorySnapshot is the JSON form of the aggregate's business state used by
// the event store's snapshots. AggregateRoot bookkeeping (ID, versions) is not
// included: the store restores it with SetInternal after unmarshalling.
//
// Every state field of VehicleInventoryAggregate must appear here; the snapshot
// round-trip test fails if one is missing.
type vehicleInventorySnapshot struct {
	DealershipID string                 `json:"dealership_id"`
	VIN          string                 `json:"vin"`
	Make         string                 `json:"make"`
	Model        string                 `json:"model"`
	ModelYear    int                    `json:"model_year"`
	StockedOn    domain.Date            `json:"stocked_on"`
	Status       domain.InventoryStatus `json:"status"`
	SoldOn       domain.Date            `json:"sold_on"`
	ActiveAction *Action                `json:"active_action"`
}

func (a *VehicleInventoryAggregate) MarshalJSON() ([]byte, error) {
	return json.Marshal(vehicleInventorySnapshot{
		DealershipID: a.dealershipID,
		VIN:          a.vin,
		Make:         a.make,
		Model:        a.model,
		ModelYear:    a.modelYear,
		StockedOn:    a.stockedOn,
		Status:       a.status,
		SoldOn:       a.soldOn,
		ActiveAction: a.activeAction,
	})
}

func (a *VehicleInventoryAggregate) UnmarshalJSON(data []byte) error {
	var s vehicleInventorySnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	a.dealershipID = s.DealershipID
	a.vin = s.VIN
	a.make = s.Make
	a.model = s.Model
	a.modelYear = s.ModelYear
	a.stockedOn = s.StockedOn
	a.status = s.Status
	a.soldOn = s.SoldOn
	a.activeAction = s.ActiveAction
	return nil
}
