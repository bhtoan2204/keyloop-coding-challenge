package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/application/query"
)

// DealershipRepository reads the dealership reference data, which lives in
// event_store.dealership (the read model does not copy it).
type DealershipRepository struct {
	db *gorm.DB
}

func NewDealershipRepository(db *gorm.DB) DealershipRepository {
	return DealershipRepository{db: db}
}

func (r DealershipRepository) Location(ctx context.Context, dealershipID string) (*time.Location, error) {
	var zone string
	err := r.db.WithContext(ctx).
		Raw(`SELECT timezone FROM event_store.dealership WHERE dealership_id = ?`, dealershipID).
		Row().
		Scan(&zone)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", query.ErrDealershipNotFound, dealershipID)
	}
	if err != nil {
		return nil, fmt.Errorf("load dealership %s: %w", dealershipID, err)
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("dealership %s has invalid time zone %q: %w", dealershipID, zone, err)
	}
	return loc, nil
}
