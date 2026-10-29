package queries

import (
	"context"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
)

// Service is the read side used by the query endpoints (*query.Service). Responses
// come from the PostgreSQL read model, which is eventually consistent with the event
// store: the projector applies the change feed continuously.
type Service interface {
	ListInventory(ctx context.Context, q query.ListInventory) (*query.InventoryPage, error)
	GetVehicle(ctx context.Context, dealershipID, vehicleID string, asOf *domain.Date) (*query.VehicleResult, error)
	GetActionHistory(ctx context.Context, dealershipID, vehicleID string, limit int, cursor string) (*query.HistoryPage, error)
	GetInventorySummary(ctx context.Context, dealershipID string, asOf *domain.Date) (*query.SummaryResult, error)
}
