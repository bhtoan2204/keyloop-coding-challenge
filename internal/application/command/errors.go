package command

import "errors"

// Application errors (domain errors from internal/domain pass through unchanged).
var (
	ErrDealershipNotFound = errors.New("dealership not found")
	// ErrVehicleNotFound also covers a vehicle owned by another dealership, so a URL
	// cannot be used to probe other dealerships' stock.
	ErrVehicleNotFound = errors.New("vehicle not found")
	// ErrVersionMismatch means the caller's expected version (If-Match) is stale.
	ErrVersionMismatch = errors.New("aggregate version mismatch")
)

// Store error contract. VehicleStore implementations report storage outcomes with
// these sentinels (wrapping the cause), so this layer does not depend on a store.
var (
	// ErrStreamNotFound: Get found no vehicle stream under the ID.
	ErrStreamNotFound = errors.New("stream not found")
	// ErrConflict: optimistic concurrency conflict; nothing was written.
	ErrConflict = errors.New("concurrent modification")
	// ErrTransient: deadlock or lock wait timeout; the transaction was rolled back.
	ErrTransient = errors.New("transient storage failure")
	// ErrUnavailable: storage unreachable or the connection broke; a write's outcome
	// is unknown, so it must not be retried automatically.
	ErrUnavailable = errors.New("storage unavailable")
)

// IsRetryable reports a save failure after which reloading and re-running the
// command may succeed: an optimistic conflict or a rolled-back transient failure.
func IsRetryable(err error) bool {
	return errors.Is(err, ErrConflict) || errors.Is(err, ErrTransient)
}
