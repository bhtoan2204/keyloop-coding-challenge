package domain

import (
	"errors"
	"fmt"
)

var (
	ErrUnknownEventType = errors.New("unknown event type")

	// Command rejections. Callers match them with errors.Is.
	ErrValidation            = errors.New("validation failed")
	ErrVehicleNotStocked     = errors.New("vehicle not stocked")
	ErrVehicleAlreadyStocked = errors.New("vehicle already stocked")
	ErrVehicleNotInStock     = errors.New("vehicle not in stock")
	ErrVehicleAlreadySold    = errors.New("vehicle already sold")
	ErrVehicleNotAging       = errors.New("vehicle not aging")
	ErrActionAlreadyActive   = errors.New("action already active")
	ErrActionNotActive       = errors.New("action not active")

	// ErrInvalidEventSequence means an event cannot follow the current state.
	// Commands never produce such a sequence, so on replay it signals corrupt history.
	ErrInvalidEventSequence = errors.New("invalid event sequence")
)

// ValidationError reports one invalid command field. It matches ErrValidation.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func (e *ValidationError) Is(target error) bool {
	return target == ErrValidation
}

func Invalid(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}
