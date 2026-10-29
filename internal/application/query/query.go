// Package query contains the read-side application services. They read the
// PostgreSQL read model only (never the MySQL event store) and are independent of the
// command side. The read model is eventually consistent with the event store.
package query

import (
	"context"
	"errors"
	"time"

	"keyloop-coding-challenge/internal/domain"
)

var (
	ErrDealershipNotFound = errors.New("dealership not found")
	// ErrVehicleNotFound also covers a vehicle of another dealership.
	ErrVehicleNotFound = errors.New("vehicle not found")
	// ErrInvalidCursor: the cursor is malformed or was issued for other filters,
	// another sort or another business date.
	ErrInvalidCursor = errors.New("invalid cursor")
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

// History change kinds.
const (
	ChangeProposed     = "PROPOSED"
	ChangeRevised      = "REVISED"
	ChangeCancelled    = "CANCELLED"
	ChangeClosedBySale = "CLOSED_BY_SALE"
)

type HistoryEntry struct {
	Version       int
	EventID       int64
	ActionID      string
	Change        string
	ActionType    domain.ActionType
	Note          string
	Reason        string
	ActorID       string
	CorrelationID string
	OccurredAt    time.Time
}

type Summary struct {
	InStock            int
	Aging              int
	AgingWithoutAction int
	OldestStockedOn    domain.Date // zero when nothing is in stock
}

// Keyset is the position after the last row of a page: (stocked_on, vehicle_id).
type Keyset struct {
	StockedOn domain.Date
	VehicleID string
}

// VehicleFilter is the validated, SQL-ready form of a list request. Every field is a
// value, never SQL text; the read model chooses predicates and ordering from it.
type VehicleFilter struct {
	DealershipID string
	Statuses     []domain.InventoryStatus // empty means all statuses
	Make         string                   // case-insensitive exact match, "" for any
	Model        string
	MinAgeDays   *int
	MaxAgeDays   *int
	AgingOnly    bool
	BusinessDate domain.Date
	// Descending orders youngest first (stocked_on DESC); default is oldest first.
	Descending bool
	After      *Keyset
	Limit      int
}

// ReadModel is the read-side persistence port (implemented by viewstore.Postgres).
type ReadModel interface {
	DealershipLocation(ctx context.Context, dealershipID string) (*time.Location, error)
	ListVehicles(ctx context.Context, f VehicleFilter) ([]Vehicle, error)
	GetVehicle(ctx context.Context, dealershipID, vehicleID string) (*Vehicle, error)
	ListHistory(ctx context.Context, vehicleID string, afterVersion, limit int) ([]HistoryEntry, error)
	Summary(ctx context.Context, dealershipID string, businessDate domain.Date) (Summary, error)
}

type Clock interface {
	Now() time.Time
}
