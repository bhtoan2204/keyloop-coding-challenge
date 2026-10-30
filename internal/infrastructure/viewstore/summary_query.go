package viewstore

import (
	"context"
	"database/sql"
	"fmt"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
)

func (p *Postgres) Summary(ctx context.Context, dealershipID string, businessDate domain.Date) (query.Summary, error) {
	cutoff := businessDate.AddDays(-(domain.AgingThresholdDays + 1)).String()
	var (
		s      query.Summary
		oldest sql.NullTime
	)
	err := p.db.WithContext(ctx).Raw(`
		SELECT count(*) FILTER (WHERE status = 'IN_STOCK'),
		       count(*) FILTER (WHERE status = 'IN_STOCK' AND stocked_on <= CAST(? AS date)),
		       count(*) FILTER (WHERE status = 'IN_STOCK' AND stocked_on <= CAST(? AS date) AND action_id IS NULL),
		       min(stocked_on) FILTER (WHERE status = 'IN_STOCK')
		FROM read_model.inventory_vehicle WHERE dealership_id = ?`, cutoff, cutoff, dealershipID).
		Row().Scan(&s.InStock, &s.Aging, &s.AgingWithoutAction, &oldest)
	if err != nil {
		return s, fmt.Errorf("inventory summary: %w", err)
	}
	if oldest.Valid {
		s.OldestStockedOn = dateOf(oldest.Time)
	}
	return s, nil
}
