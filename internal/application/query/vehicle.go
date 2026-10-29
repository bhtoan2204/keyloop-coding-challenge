package query

import (
	"time"

	"keyloop-coding-challenge/internal/domain"
)

// Vehicle is a vehicle as projected in the read model.
type Vehicle struct {
	VehicleID    string
	DealershipID string
	VIN          string
	Make         string
	Model        string
	ModelYear    int
	StockedOn    domain.Date
	Status       domain.InventoryStatus
	SoldOn       domain.Date
	Action       *Action
	// Version is the last aggregate version applied to this row.
	Version   int
	UpdatedAt time.Time
}

type Action struct {
	ID         string
	Type       domain.ActionType
	Note       string
	ProposedAt time.Time
	UpdatedAt  time.Time
	UpdatedBy  string
}

// AgeDays is the age at businessDate for an in-stock vehicle, and the age at sale for
// a sold one. Stock-in day is day 0.
func (v Vehicle) AgeDays(businessDate domain.Date) int {
	if v.Status == domain.StatusSold {
		return domain.AgeInDays(v.StockedOn, v.SoldOn)
	}
	return domain.AgeInDays(v.StockedOn, businessDate)
}

// IsAging: in stock and more than AgingThresholdDays old (exactly 90 is not aging).
func (v Vehicle) IsAging(businessDate domain.Date) bool {
	return v.Status == domain.StatusInStock && v.AgeDays(businessDate) > domain.AgingThresholdDays
}
