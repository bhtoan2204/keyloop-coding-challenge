package query

import (
	"context"
	"time"

	"keyloop-coding-challenge/internal/domain"
)

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
