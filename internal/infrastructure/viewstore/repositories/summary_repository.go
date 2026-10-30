package repositories

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/infrastructure/viewstore/models"
)

// InventorySummaryRepository computes the dealership's stock counts in one scan, with
// the same dealership scope and aging cutoff as the list query.
type InventorySummaryRepository struct {
	db *gorm.DB
}

func NewInventorySummaryRepository(db *gorm.DB) InventorySummaryRepository {
	return InventorySummaryRepository{db: db}
}

func (r InventorySummaryRepository) Summary(ctx context.Context, dealershipID string, businessDate domain.Date) (query.Summary, error) {
	cutoff := agingCutoff(businessDate).String()
	var (
		s      query.Summary
		oldest models.NullDate
	)
	err := r.db.WithContext(ctx).
		Model(&models.InventoryVehicle{}).
		Scopes(inDealership(dealershipID)).
		Select(`count(*) FILTER (WHERE `+inStockPredicate+`),
			count(*) FILTER (WHERE `+inStockPredicate+` AND stocked_on <= CAST(? AS date)),
			count(*) FILTER (WHERE `+inStockPredicate+` AND stocked_on <= CAST(? AS date) AND action_id IS NULL),
			min(stocked_on) FILTER (WHERE `+inStockPredicate+`)`, cutoff, cutoff).
		Row().
		Scan(&s.InStock, &s.Aging, &s.AgingWithoutAction, &oldest)
	if err != nil {
		return s, fmt.Errorf("inventory summary: %w", err)
	}
	s.OldestStockedOn = fromNullDate(oldest)
	return s, nil
}
