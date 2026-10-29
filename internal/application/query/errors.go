package query

import "errors"

var (
	ErrDealershipNotFound = errors.New("dealership not found")
	// ErrVehicleNotFound also covers a vehicle of another dealership.
	ErrVehicleNotFound = errors.New("vehicle not found")
	// ErrInvalidCursor: the cursor is malformed or was issued for other filters,
	// another sort or another business date.
	ErrInvalidCursor = errors.New("invalid cursor")
)
