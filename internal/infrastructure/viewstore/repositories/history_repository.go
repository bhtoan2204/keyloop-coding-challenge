package repositories

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/application/projection"
	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/infrastructure/viewstore/models"
)

// ActionHistoryRepository appends to and pages through read_model.vehicle_action_history.
type ActionHistoryRepository struct {
	db *gorm.DB
}

func NewActionHistoryRepository(db *gorm.DB) ActionHistoryRepository {
	return ActionHistoryRepository{db: db}
}

func (r ActionHistoryRepository) Append(ctx context.Context, e projection.ActionHistoryEntry) error {
	row := toActionHistoryRow(e)
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("append history %s v%d: %w", e.VehicleID, e.Version, err)
	}
	return nil
}

// List returns up to limit entries after afterVersion, oldest first.
func (r ActionHistoryRepository) List(ctx context.Context, vehicleID string, afterVersion, limit int) ([]query.HistoryEntry, error) {
	var rows []models.VehicleActionHistory
	err := r.db.WithContext(ctx).
		Where("vehicle_id = ? AND version > ?", vehicleID, afterVersion).
		Order("version").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list history: %w", err)
	}
	out := make([]query.HistoryEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, toQueryHistoryEntry(row))
	}
	return out, nil
}
