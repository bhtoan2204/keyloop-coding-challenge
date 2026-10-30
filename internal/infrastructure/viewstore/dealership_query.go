package viewstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"keyloop-coding-challenge/internal/application/query"
)

func (p *Postgres) DealershipLocation(ctx context.Context, dealershipID string) (*time.Location, error) {
	var zone string
	err := p.db.WithContext(ctx).Raw(`SELECT timezone FROM event_store.dealership WHERE dealership_id = ?`, dealershipID).Row().Scan(&zone)
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
