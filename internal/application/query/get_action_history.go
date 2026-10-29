package query

import (
	"context"
	"fmt"
)

type HistoryPage struct {
	Entries    []HistoryEntry
	Limit      int
	HasMore    bool
	NextCursor string
}

type historyCursor struct {
	Version   int    `json:"v"`
	VehicleID string `json:"vid"`
	After     int    `json:"after"`
}

// GetActionHistory returns the vehicle's action changes, oldest first.
func (s *Service) GetActionHistory(ctx context.Context, dealershipID, vehicleID string, limit int, cursor string) (*HistoryPage, error) {
	limit, err := validateLimit(limit)
	if err != nil {
		return nil, err
	}
	after := 0
	if cursor != "" {
		var c historyCursor
		if err := decode(cursor, &c); err != nil || c.Version != 1 || c.VehicleID != vehicleID || c.After < 1 {
			return nil, fmt.Errorf("%w: not a history cursor for this vehicle", ErrInvalidCursor)
		}
		after = c.After
	}
	if _, err := s.dealershipLocation(ctx, dealershipID); err != nil {
		return nil, err
	}
	// Ownership: the vehicle must belong to this dealership.
	if _, err := s.rm.GetVehicle(ctx, dealershipID, vehicleID); err != nil {
		return nil, err
	}
	entries, err := s.rm.ListHistory(ctx, vehicleID, after, limit+1)
	if err != nil {
		return nil, err
	}
	page := &HistoryPage{Limit: limit}
	if len(entries) > limit {
		entries = entries[:limit]
		page.HasMore = true
		page.NextCursor = encode(historyCursor{
			Version:   1,
			VehicleID: vehicleID,
			After:     entries[len(entries)-1].Version,
		})
	}
	page.Entries = entries
	return page, nil
}
