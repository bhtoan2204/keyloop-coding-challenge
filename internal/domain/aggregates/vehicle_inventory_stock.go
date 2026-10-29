package aggregates

import (
	"fmt"
	"strings"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
)

type StockVehicle struct {
	DealershipID string
	VIN          string
	Make         string
	Model        string
	ModelYear    int
	StockedOn    domain.Date
}

// Stock records the vehicle entering inventory. The aggregate ID must already be set.
func (a *VehicleInventoryAggregate) Stock(cmd StockVehicle, businessDate domain.Date) error {
	if a.AggregateID() == "" {
		return domain.Invalid("vehicle_id", "is required")
	}
	if a.IsStocked() {
		return fmt.Errorf("%w: vehicle %s", domain.ErrVehicleAlreadyStocked, a.AggregateID())
	}
	dealershipID := strings.TrimSpace(cmd.DealershipID)
	if dealershipID == "" {
		return domain.Invalid("dealership_id", "is required")
	}
	vin := strings.ToUpper(strings.TrimSpace(cmd.VIN))
	if !vinPattern.MatchString(vin) {
		return domain.Invalid("vin", "must be 17 characters A-Z/0-9 excluding I, O and Q")
	}
	mk, err := requiredName("make", cmd.Make)
	if err != nil {
		return err
	}
	model, err := requiredName("model", cmd.Model)
	if err != nil {
		return err
	}
	if maxYear := businessDate.Year() + 1; cmd.ModelYear < minModelYear || cmd.ModelYear > maxYear {
		return domain.Invalid("model_year", fmt.Sprintf("must be between %d and %d", minModelYear, maxYear))
	}
	if cmd.StockedOn.IsZero() {
		return domain.Invalid("stocked_on", "is required")
	}
	if cmd.StockedOn.After(businessDate) {
		return domain.Invalid("stocked_on", fmt.Sprintf("must not be after the business date %s", businessDate))
	}
	return a.ApplyChange(a, &events.VehicleStockedEvent{
		DealershipID: dealershipID,
		VIN:          vin,
		Make:         mk,
		Model:        model,
		ModelYear:    cmd.ModelYear,
		StockedOn:    cmd.StockedOn,
	})
}

func (a *VehicleInventoryAggregate) applyVehicleStockedEvent(e *events.VehicleStockedEvent) error {
	if a.IsStocked() {
		return a.invalidSequence("VehicleStockedEvent", "vehicle is already stocked")
	}
	if e.DealershipID == "" || e.VIN == "" || e.StockedOn.IsZero() {
		return a.invalidSequence("VehicleStockedEvent", "dealership_id, vin and stocked_on are required")
	}
	a.dealershipID = e.DealershipID
	a.vin = e.VIN
	a.make = e.Make
	a.model = e.Model
	a.modelYear = e.ModelYear
	a.stockedOn = e.StockedOn
	a.status = domain.StatusInStock
	return nil
}
