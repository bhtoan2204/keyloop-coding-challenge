package query

import (
	"context"

	"keyloop-coding-challenge/internal/domain"
)

type Summary struct {
	InStock            int
	Aging              int
	AgingWithoutAction int
	OldestStockedOn    domain.Date // zero when nothing is in stock
}

type SummaryResult struct {
	Summary
	AsOf domain.Date
}

func (s *Service) GetInventorySummary(ctx context.Context, dealershipID string, asOf *domain.Date) (*SummaryResult, error) {
	businessDate, err := s.businessDate(ctx, dealershipID, asOf)
	if err != nil {
		return nil, err
	}
	sum, err := s.rm.Summary(ctx, dealershipID, businessDate)
	if err != nil {
		return nil, err
	}
	return &SummaryResult{Summary: sum, AsOf: businessDate}, nil
}
