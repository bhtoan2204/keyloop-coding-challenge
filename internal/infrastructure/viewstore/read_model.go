package viewstore

import (
	"context"
	"time"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/infrastructure/viewstore/repositories"
)

var _ query.ReadModel = (*ReadModel)(nil)

// ReadModel serves the query services from the read-only connection pool.
type ReadModel struct {
	vehicles    repositories.VehicleRepository
	history     repositories.ActionHistoryRepository
	summary     repositories.InventorySummaryRepository
	dealerships repositories.DealershipRepository
}

func NewReadModel(db *gorm.DB) *ReadModel {
	return &ReadModel{
		vehicles:    repositories.NewVehicleRepository(db),
		history:     repositories.NewActionHistoryRepository(db),
		summary:     repositories.NewInventorySummaryRepository(db),
		dealerships: repositories.NewDealershipRepository(db),
	}
}

func (m *ReadModel) DealershipLocation(ctx context.Context, dealershipID string) (*time.Location, error) {
	return m.dealerships.Location(ctx, dealershipID)
}

func (m *ReadModel) ListVehicles(ctx context.Context, f query.VehicleFilter) ([]query.Vehicle, error) {
	return m.vehicles.List(ctx, f)
}

func (m *ReadModel) GetVehicle(ctx context.Context, dealershipID, vehicleID string) (*query.Vehicle, error) {
	return m.vehicles.InDealership(ctx, dealershipID, vehicleID)
}

func (m *ReadModel) ListHistory(ctx context.Context, vehicleID string, afterVersion, limit int) ([]query.HistoryEntry, error) {
	return m.history.List(ctx, vehicleID, afterVersion, limit)
}

func (m *ReadModel) Summary(ctx context.Context, dealershipID string, businessDate domain.Date) (query.Summary, error) {
	return m.summary.Summary(ctx, dealershipID, businessDate)
}
