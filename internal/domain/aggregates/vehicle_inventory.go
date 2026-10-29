package aggregates

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
	"keyloop-coding-challenge/package/eventsourcing"
)

var _ eventsourcing.Aggregate = (*VehicleInventoryAggregate)(nil)

const (
	maxNameLen   = 64
	maxNoteLen   = 1000
	minModelYear = 1900
)

// VINs are 17 characters and never contain I, O or Q.
var vinPattern = regexp.MustCompile(`^[A-HJ-NPR-Z0-9]{17}$`)

// Action is the single active action recorded for a vehicle.
type Action struct {
	ID   string            `json:"id"`
	Type domain.ActionType `json:"type"`
	Note string            `json:"note"`
}

// VehicleInventoryAggregate is one vehicle's inventory lifecycle at one dealership.
// The vehicle ID is the aggregate ID.
//
// Command methods validate business rules and record events through ApplyChange.
// State changes only in the apply methods called from Transition, which also run on
// replay; they re-check event ordering so invalid history is rejected.
type VehicleInventoryAggregate struct {
	eventsourcing.AggregateRoot

	dealershipID string
	vin          string
	make         string
	model        string
	modelYear    int
	stockedOn    domain.Date
	status       domain.InventoryStatus // empty until stocked
	soldOn       domain.Date
	activeAction *Action
}

func (a *VehicleInventoryAggregate) RegisterEvents(register eventsourcing.RegisterEventsFunc) error {
	return register(
		&events.VehicleStockedEvent{},
		&events.VehicleSoldEvent{},
		&events.VehicleActionProposedEvent{},
		&events.VehicleActionRevisedEvent{},
		&events.VehicleActionCancelledEvent{},
	)
}

func (a *VehicleInventoryAggregate) Transition(e eventsourcing.Event) error {
	switch event := e.Data.(type) {
	case *events.VehicleStockedEvent:
		return a.applyVehicleStockedEvent(event)
	case *events.VehicleSoldEvent:
		return a.applyVehicleSoldEvent(event)
	case *events.VehicleActionProposedEvent:
		return a.applyVehicleActionProposedEvent(event)
	case *events.VehicleActionRevisedEvent:
		return a.applyVehicleActionRevisedEvent(event)
	case *events.VehicleActionCancelledEvent:
		return a.applyVehicleActionCancelledEvent(event)
	default:
		return domain.ErrUnknownEventType
	}
}

func (a *VehicleInventoryAggregate) VehicleID() string              { return a.AggregateID() }
func (a *VehicleInventoryAggregate) DealershipID() string           { return a.dealershipID }
func (a *VehicleInventoryAggregate) VIN() string                    { return a.vin }
func (a *VehicleInventoryAggregate) Make() string                   { return a.make }
func (a *VehicleInventoryAggregate) Model() string                  { return a.model }
func (a *VehicleInventoryAggregate) ModelYear() int                 { return a.modelYear }
func (a *VehicleInventoryAggregate) StockedOn() domain.Date         { return a.stockedOn }
func (a *VehicleInventoryAggregate) Status() domain.InventoryStatus { return a.status }
func (a *VehicleInventoryAggregate) SoldOn() domain.Date            { return a.soldOn }
func (a *VehicleInventoryAggregate) IsStocked() bool                { return a.status != "" }

// ActiveAction returns a copy of the active action, if any.
func (a *VehicleInventoryAggregate) ActiveAction() (Action, bool) {
	if a.activeAction == nil {
		return Action{}, false
	}
	return *a.activeAction, true
}

// AgeInDays is the number of calendar days in stock at businessDate. For a sold
// vehicle the age stops at the sale date.
func (a *VehicleInventoryAggregate) AgeInDays(businessDate domain.Date) int {
	if a.status == domain.StatusSold {
		return domain.AgeInDays(a.stockedOn, a.soldOn)
	}
	return domain.AgeInDays(a.stockedOn, businessDate)
}

// IsAging reports whether the vehicle is in stock and has been for more than
// AgingThresholdDays calendar days at businessDate.
func (a *VehicleInventoryAggregate) IsAging(businessDate domain.Date) bool {
	return a.status == domain.StatusInStock &&
		domain.AgeInDays(a.stockedOn, businessDate) > domain.AgingThresholdDays
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

// ProposeAction records a new action. Only an in-stock, aging vehicle without an
// active action qualifies.
func (a *VehicleInventoryAggregate) ProposeAction(cmd ProposeAction, businessDate domain.Date) error {
	if err := a.requireInStock(); err != nil {
		return err
	}
	if strings.TrimSpace(cmd.ActionID) == "" {
		return domain.Invalid("action_id", "is required")
	}
	note, err := validateAction(cmd.ActionType, cmd.Note)
	if err != nil {
		return err
	}
	if a.activeAction != nil {
		return fmt.Errorf("%w: action %s", domain.ErrActionAlreadyActive, a.activeAction.ID)
	}
	if !a.IsAging(businessDate) {
		return fmt.Errorf("%w: %d days in stock at %s, more than %d required",
			domain.ErrVehicleNotAging, a.AgeInDays(businessDate), businessDate, domain.AgingThresholdDays)
	}
	return a.ApplyChange(a, &events.VehicleActionProposedEvent{
		ActionID: cmd.ActionID, ActionType: cmd.ActionType, Note: note,
	})
}

// ReviseAction replaces the type and note of the active action. A revision that
// changes nothing records no event.
func (a *VehicleInventoryAggregate) ReviseAction(cmd ReviseAction) error {
	if err := a.requireInStock(); err != nil {
		return err
	}
	note, err := validateAction(cmd.ActionType, cmd.Note)
	if err != nil {
		return err
	}
	if err := a.requireActiveAction(cmd.ActionID); err != nil {
		return err
	}
	if a.activeAction.Type == cmd.ActionType && a.activeAction.Note == note {
		return nil
	}
	return a.ApplyChange(a, &events.VehicleActionRevisedEvent{
		ActionID: cmd.ActionID, ActionType: cmd.ActionType, Note: note,
	})
}

// CancelAction withdraws the active action.
func (a *VehicleInventoryAggregate) CancelAction(cmd CancelAction) error {
	if err := a.requireInStock(); err != nil {
		return err
	}
	reason := strings.TrimSpace(cmd.Reason)
	if utf8.RuneCountInString(reason) > maxNoteLen {
		return domain.Invalid("reason", fmt.Sprintf("must be at most %d characters", maxNoteLen))
	}
	if err := a.requireActiveAction(cmd.ActionID); err != nil {
		return err
	}
	return a.ApplyChange(a, &events.VehicleActionCancelledEvent{ActionID: cmd.ActionID, Reason: reason})
}

// Sell removes the vehicle from active inventory and closes the active action, if any.
func (a *VehicleInventoryAggregate) Sell(cmd SellVehicle, businessDate domain.Date) error {
	if !a.IsStocked() {
		return fmt.Errorf("%w: vehicle %s", domain.ErrVehicleNotStocked, a.AggregateID())
	}
	if a.status == domain.StatusSold {
		return fmt.Errorf("%w: sold on %s", domain.ErrVehicleAlreadySold, a.soldOn)
	}
	switch {
	case cmd.SoldOn.IsZero():
		return domain.Invalid("sold_on", "is required")
	case cmd.SoldOn.Before(a.stockedOn):
		return domain.Invalid("sold_on", fmt.Sprintf("must not be before stocked_on %s", a.stockedOn))
	case cmd.SoldOn.After(businessDate):
		return domain.Invalid("sold_on", fmt.Sprintf("must not be after the business date %s", businessDate))
	}
	event := &events.VehicleSoldEvent{SoldOn: cmd.SoldOn}
	if a.activeAction != nil {
		event.ClosedActionID = a.activeAction.ID
	}
	return a.ApplyChange(a, event)
}

// The apply methods are the only place state changes. Each validates the event
// against the current state before mutating anything, so a rejected event leaves the
// aggregate untouched. They do no I/O and never read the clock.

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

func (a *VehicleInventoryAggregate) applyVehicleSoldEvent(e *events.VehicleSoldEvent) error {
	if err := a.inStockForEvent("VehicleSoldEvent"); err != nil {
		return err
	}
	if e.SoldOn.IsZero() {
		return a.invalidSequence("VehicleSoldEvent", "sold_on is required")
	}
	if active := a.activeActionID(); e.ClosedActionID != active {
		return a.invalidSequence("VehicleSoldEvent",
			fmt.Sprintf("closed_action_id %q does not match active action %q", e.ClosedActionID, active))
	}
	a.status = domain.StatusSold
	a.soldOn = e.SoldOn
	a.activeAction = nil
	return nil
}

func (a *VehicleInventoryAggregate) applyVehicleActionProposedEvent(e *events.VehicleActionProposedEvent) error {
	if err := a.inStockForEvent("VehicleActionProposedEvent"); err != nil {
		return err
	}
	if e.ActionID == "" {
		return a.invalidSequence("VehicleActionProposedEvent", "action_id is required")
	}
	if a.activeAction != nil {
		return a.invalidSequence("VehicleActionProposedEvent", "action "+a.activeAction.ID+" is already active")
	}
	a.activeAction = &Action{ID: e.ActionID, Type: e.ActionType, Note: e.Note}
	return nil
}

func (a *VehicleInventoryAggregate) applyVehicleActionRevisedEvent(e *events.VehicleActionRevisedEvent) error {
	if err := a.inStockForEvent("VehicleActionRevisedEvent"); err != nil {
		return err
	}
	if a.activeActionID() == "" || e.ActionID != a.activeActionID() {
		return a.invalidSequence("VehicleActionRevisedEvent", "action "+e.ActionID+" is not active")
	}
	a.activeAction = &Action{ID: e.ActionID, Type: e.ActionType, Note: e.Note}
	return nil
}

func (a *VehicleInventoryAggregate) applyVehicleActionCancelledEvent(e *events.VehicleActionCancelledEvent) error {
	if err := a.inStockForEvent("VehicleActionCancelledEvent"); err != nil {
		return err
	}
	if a.activeActionID() == "" || e.ActionID != a.activeActionID() {
		return a.invalidSequence("VehicleActionCancelledEvent", "action "+e.ActionID+" is not active")
	}
	a.activeAction = nil
	return nil
}

func (a *VehicleInventoryAggregate) inStockForEvent(eventType string) error {
	switch a.status {
	case domain.StatusInStock:
		return nil
	case domain.StatusSold:
		return a.invalidSequence(eventType, "vehicle is already sold")
	default:
		return a.invalidSequence(eventType, "vehicle is not stocked")
	}
}

func (a *VehicleInventoryAggregate) invalidSequence(eventType, reason string) error {
	return fmt.Errorf("%w: aggregate %s: %s: %s", domain.ErrInvalidEventSequence, a.AggregateID(), eventType, reason)
}

func (a *VehicleInventoryAggregate) activeActionID() string {
	if a.activeAction == nil {
		return ""
	}
	return a.activeAction.ID
}

func (a *VehicleInventoryAggregate) requireInStock() error {
	switch a.status {
	case domain.StatusInStock:
		return nil
	case domain.StatusSold:
		return fmt.Errorf("%w: vehicle %s was sold on %s", domain.ErrVehicleNotInStock, a.AggregateID(), a.soldOn)
	default:
		return fmt.Errorf("%w: vehicle %s", domain.ErrVehicleNotStocked, a.AggregateID())
	}
}

func (a *VehicleInventoryAggregate) requireActiveAction(actionID string) error {
	if a.activeAction == nil {
		return fmt.Errorf("%w: vehicle %s has no active action", domain.ErrActionNotActive, a.AggregateID())
	}
	if a.activeAction.ID != actionID {
		return fmt.Errorf("%w: action %s is not the active action", domain.ErrActionNotActive, actionID)
	}
	return nil
}

func requiredName(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", domain.Invalid(field, "is required")
	}
	if utf8.RuneCountInString(value) > maxNameLen {
		return "", domain.Invalid(field, fmt.Sprintf("must be at most %d characters", maxNameLen))
	}
	return value, nil
}

func validateAction(t domain.ActionType, note string) (string, error) {
	if !t.Valid() {
		return "", domain.Invalid("action_type", fmt.Sprintf("unknown action type %q", t))
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > maxNoteLen {
		return "", domain.Invalid("note", fmt.Sprintf("must be at most %d characters", maxNoteLen))
	}
	if t == domain.ActionOther && note == "" {
		return "", domain.Invalid("note", "is required when action_type is OTHER")
	}
	return note, nil
}
