package repositories

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"keyloop-coding-challenge/internal/application/projection"
	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/infrastructure/viewstore/models"
)

// VehicleRepository reads and writes read_model.inventory_vehicle.
type VehicleRepository struct {
	db *gorm.DB
}

func NewVehicleRepository(db *gorm.DB) VehicleRepository {
	return VehicleRepository{db: db}
}

// Projected returns the projected vehicle, or nil when there is none.
func (r VehicleRepository) Projected(ctx context.Context, vehicleID string) (*projection.ProjectedVehicle, error) {
	var row models.InventoryVehicle
	err := r.db.WithContext(ctx).Where("vehicle_id = ?", vehicleID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load vehicle %s: %w", vehicleID, err)
	}
	v := toProjectedVehicle(row)
	return &v, nil
}

// Save inserts or replaces the vehicle's row.
func (r VehicleRepository) Save(ctx context.Context, v projection.ProjectedVehicle) error {
	row := toInventoryVehicle(v)
	err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "vehicle_id"}},
			UpdateAll: true,
		}).
		Create(&row).Error
	if err != nil {
		return fmt.Errorf("save vehicle %s: %w", v.VehicleID, err)
	}
	return nil
}

// InDealership returns the vehicle if it belongs to the dealership.
func (r VehicleRepository) InDealership(ctx context.Context, dealershipID, vehicleID string) (*query.Vehicle, error) {
	var row models.InventoryVehicle
	err := r.db.WithContext(ctx).
		Scopes(inDealership(dealershipID)).
		Where("vehicle_id = ?", vehicleID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: %s", query.ErrVehicleNotFound, vehicleID)
	}
	if err != nil {
		return nil, fmt.Errorf("get vehicle: %w", err)
	}
	v := toQueryVehicle(row)
	return &v, nil
}

// List returns one page of the dealership's vehicles in age order.
func (r VehicleRepository) List(ctx context.Context, f query.VehicleFilter) ([]query.Vehicle, error) {
	var rows []models.InventoryVehicle
	if err := listVehicles(r.db.WithContext(ctx), f).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	out := make([]query.Vehicle, 0, len(rows))
	for _, row := range rows {
		out = append(out, toQueryVehicle(row))
	}
	return out, nil
}

// listVehicles composes the list query; separate from List so its SQL can be inspected.
func listVehicles(db *gorm.DB, f query.VehicleFilter) *gorm.DB {
	return db.Model(&models.InventoryVehicle{}).
		Scopes(
			inDealership(f.DealershipID),
			withStatus(f.Statuses),
			withMake(f.Make),
			withModel(f.Model),
			withAgeBounds(f),
			agingOnly(f.AgingOnly, f.BusinessDate),
			afterKeyset(f.After, f.Descending),
			orderByAge(f.Descending),
		).
		Limit(f.Limit)
}
