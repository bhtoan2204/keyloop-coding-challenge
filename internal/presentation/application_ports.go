package presentation

import (
	"context"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
)

// CommandService is implemented by *command.Service.
type CommandService interface {
	StockVehicle(ctx context.Context, cmd command.StockVehicle, meta command.Meta) (*command.Result, error)
	ProposeAction(ctx context.Context, cmd command.ProposeAction, meta command.Meta) (*command.Result, error)
	ReviseAction(ctx context.Context, cmd command.ReviseAction, meta command.Meta) (*command.Result, error)
	CancelAction(ctx context.Context, cmd command.CancelAction, meta command.Meta) (*command.Result, error)
	SellVehicle(ctx context.Context, cmd command.SellVehicle, meta command.Meta) (*command.Result, error)
}

// ReadinessCheck reports whether the dependencies of the write API are reachable.
type ReadinessCheck func(ctx context.Context) error

// QueryService is implemented by *query.Service. Responses come from the PostgreSQL
// read model, which is eventually consistent with the event store (in Phase 4 it is
// refreshed by the sync-projections command, not continuously).
type QueryService interface {
	ListInventory(ctx context.Context, q query.ListInventory) (*query.InventoryPage, error)
	GetVehicle(ctx context.Context, dealershipID, vehicleID string, asOf *domain.Date) (*query.VehicleResult, error)
	GetActionHistory(ctx context.Context, dealershipID, vehicleID string, limit int, cursor string) (*query.HistoryPage, error)
	GetInventorySummary(ctx context.Context, dealershipID string, asOf *domain.Date) (*query.SummaryResult, error)
}
