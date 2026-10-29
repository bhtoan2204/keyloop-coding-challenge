// Package referencedata reads reference data owned by other systems; the write side
// only holds a read-only copy (seeded by repos.Migrate, version 3).
package referencedata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"keyloop-coding-challenge/internal/application/command"

	"gorm.io/gorm"
)

// Dealerships implements command.DealershipDirectory on the dealership table.
type Dealerships struct {
	db *gorm.DB
}

func NewDealerships(db *gorm.DB) *Dealerships {
	return &Dealerships{db: db}
}

func (d *Dealerships) Location(ctx context.Context, dealershipID string) (*time.Location, error) {
	var timezone string
	err := d.db.WithContext(ctx).Raw(`SELECT timezone FROM dealership WHERE dealership_id = ?`, dealershipID).
		Row().Scan(&timezone)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", command.ErrDealershipNotFound, dealershipID)
	}
	if err != nil {
		return nil, fmt.Errorf("load dealership %s: %w", dealershipID, err)
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("dealership %s has invalid time zone %q: %w", dealershipID, timezone, err)
	}
	return loc, nil
}
