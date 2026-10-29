package query

import (
	"context"
	"fmt"
	"time"

	"keyloop-coding-challenge/internal/domain"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100

	SortAgeDesc = "age_desc" // oldest stock first (default)
	SortAgeAsc  = "age_asc"

	StatusAll = "ALL"
)

type Service struct {
	rm    ReadModel
	clock Clock
}

func NewService(rm ReadModel, clock Clock) *Service {
	return &Service{rm: rm, clock: clock}
}

// businessDate is the pinned date when given, else today in the dealership's zone.
// It also verifies that the dealership exists.
func (s *Service) businessDate(ctx context.Context, dealershipID string, pinned *domain.Date) (domain.Date, error) {
	loc, err := s.dealershipLocation(ctx, dealershipID)
	if err != nil {
		return domain.Date{}, err
	}
	if pinned != nil {
		return *pinned, nil
	}
	return domain.BusinessDate(s.clock.Now(), loc), nil
}

func (s *Service) dealershipLocation(ctx context.Context, dealershipID string) (*time.Location, error) {
	return s.rm.DealershipLocation(ctx, dealershipID)
}

func validateLimit(limit int) (int, error) {
	switch {
	case limit == 0:
		return DefaultLimit, nil
	case limit < 1 || limit > MaxLimit:
		return 0, domain.Invalid("limit", fmt.Sprintf("must be between 1 and %d", MaxLimit))
	}
	return limit, nil
}

type Clock interface {
	Now() time.Time
}
