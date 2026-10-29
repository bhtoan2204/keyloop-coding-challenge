package domain

// InventoryStatus is the lifecycle state of a stocked vehicle.
type InventoryStatus string

const (
	StatusInStock InventoryStatus = "IN_STOCK"
	StatusSold    InventoryStatus = "SOLD"
)

// ActionType is what a manager proposes to do with an aging vehicle.
type ActionType string

const (
	ActionPriceReduction ActionType = "PRICE_REDUCTION"
	ActionPromote        ActionType = "PROMOTE"
	ActionTransfer       ActionType = "TRANSFER"
	ActionAuction        ActionType = "AUCTION"
	ActionWholesale      ActionType = "WHOLESALE"
	ActionHold           ActionType = "HOLD"
	ActionOther          ActionType = "OTHER"
)

func (t ActionType) Valid() bool {
	switch t {
	case ActionPriceReduction, ActionPromote, ActionTransfer, ActionAuction,
		ActionWholesale, ActionHold, ActionOther:
		return true
	}
	return false
}

// AgingThresholdDays is the age a vehicle must exceed to be aging stock:
// exactly 90 days is not aging, 91 is.
const AgingThresholdDays = 90

// AgeInDays is the number of calendar days from stock-in to businessDate.
// The stock-in day itself is day 0.
func AgeInDays(stockedOn, businessDate Date) int {
	return businessDate.DaysSince(stockedOn)
}
