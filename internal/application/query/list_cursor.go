package query

import (
	"fmt"

	"keyloop-coding-challenge/internal/domain"
)

// listCursor is the opaque keyset cursor. It pins the business date and records a
// fingerprint of the filters and sort, so a page cannot be continued with different
// parameters or across a dealership-local midnight. It is not signed: it only selects
// rows the caller could request directly anyway.
type listCursor struct {
	Version     int    `json:"v"`
	Sort        string `json:"s"`
	AsOf        string `json:"a"`
	Fingerprint string `json:"f"`
	StockedOn   string `json:"k1"`
	VehicleID   string `json:"k2"`
}

func decodeListCursor(s string) (listCursor, error) {
	var c listCursor
	if err := decode(s, &c); err != nil || c.Version != 1 {
		return c, fmt.Errorf("%w: malformed", ErrInvalidCursor)
	}
	return c, nil
}

// resolveListCursor validates the request's cursor against its sort and filter
// fingerprint and returns the pinned business date and keyset position. Without a
// cursor the business date is the request's as_of (or nil for today).
func resolveListCursor(q ListInventory, sortKey, fingerprint string) (*domain.Date, *Keyset, error) {
	if q.Cursor == "" {
		return q.AsOf, nil, nil
	}
	c, err := decodeListCursor(q.Cursor)
	if err != nil {
		return nil, nil, err
	}
	asOf, err := domain.ParseDate(c.AsOf)
	if err != nil || c.Sort != sortKey || c.Fingerprint != fingerprint {
		return nil, nil, fmt.Errorf("%w: it was issued for other filters or another sort", ErrInvalidCursor)
	}
	if q.AsOf != nil && *q.AsOf != asOf {
		return nil, nil, fmt.Errorf("%w: as_of differs from the cursor's", ErrInvalidCursor)
	}
	stockedOn, err := domain.ParseDate(c.StockedOn)
	if err != nil || c.VehicleID == "" {
		return nil, nil, fmt.Errorf("%w: malformed position", ErrInvalidCursor)
	}
	after := &Keyset{
		StockedOn: stockedOn,
		VehicleID: c.VehicleID,
	}
	return &asOf, after, nil
}
