package presentation

import (
	"context"
	"errors"
	"net/http"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
)

const codeUnavailable = "UNAVAILABLE"

// domainErrors maps known business and application errors to HTTP. Their messages
// contain only identifiers and dates from the request, so they are returned as detail.
var domainErrors = []struct {
	err    error
	status int
	code   string
	title  string
}{
	{command.ErrDealershipNotFound, http.StatusNotFound, "DEALERSHIP_NOT_FOUND", "Dealership not found"},
	{query.ErrDealershipNotFound, http.StatusNotFound, "DEALERSHIP_NOT_FOUND", "Dealership not found"},
	{query.ErrVehicleNotFound, http.StatusNotFound, "VEHICLE_NOT_FOUND", "Vehicle not found"},
	{query.ErrInvalidCursor, http.StatusBadRequest, "INVALID_CURSOR", "Invalid cursor"},
	{command.ErrVehicleNotFound, http.StatusNotFound, "VEHICLE_NOT_FOUND", "Vehicle not found"},
	{domain.ErrVehicleNotStocked, http.StatusNotFound, "VEHICLE_NOT_FOUND", "Vehicle not found"},
	{command.ErrVersionMismatch, http.StatusPreconditionFailed, "VERSION_MISMATCH", "Vehicle version does not match If-Match"},
	{domain.ErrVehicleAlreadyStocked, http.StatusConflict, "VEHICLE_ALREADY_EXISTS", "Vehicle already exists"},
	{domain.ErrVehicleNotInStock, http.StatusConflict, "VEHICLE_NOT_IN_STOCK", "Vehicle is not in stock"},
	{domain.ErrVehicleAlreadySold, http.StatusConflict, "VEHICLE_ALREADY_SOLD", "Vehicle is already sold"},
	{domain.ErrVehicleNotAging, http.StatusConflict, "VEHICLE_NOT_AGING", "Vehicle is not aging"},
	{domain.ErrActionAlreadyActive, http.StatusConflict, "ACTION_ALREADY_ACTIVE", "An action is already active"},
	{domain.ErrActionNotActive, http.StatusConflict, "ACTION_NOT_ACTIVE", "Action is not active"},
}

// problemFor classifies err into status, code, title and client-safe detail.
func problemFor(err error) problem {
	var apiErr *apiError
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &apiErr):
		return problem{
			Status: apiErr.status,
			Code:   apiErr.code,
			Title:  http.StatusText(apiErr.status),
			Detail: apiErr.detail,
			Errors: apiErr.fields,
		}
	case errors.As(err, &validation):
		return problem{
			Status: http.StatusBadRequest,
			Code:   "VALIDATION_ERROR",
			Title:  "Request validation failed",
			Detail: "request validation failed",
			Errors: []fieldError{{Field: validation.Field, Message: validation.Reason}},
		}
	case errors.Is(err, command.ErrVersionMismatch):
		// Checked before the storage cases: a stale If-Match is the caller's answer.
		return problem{
			Status: http.StatusPreconditionFailed,
			Code:   "VERSION_MISMATCH",
			Title:  "Vehicle version does not match If-Match",
			Detail: "the vehicle was changed; reload it and retry with the current ETag",
		}
	case errors.Is(err, command.ErrConflict):
		return problem{
			Status: http.StatusConflict,
			Code:   "CONCURRENCY_CONFLICT",
			Title:  "Concurrent update",
			Detail: "the vehicle kept changing while the command was retried; reload and try again",
		}
	case errors.Is(err, command.ErrTransient), errors.Is(err, command.ErrUnavailable),
		errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return problem{
			Status: http.StatusServiceUnavailable,
			Code:   codeUnavailable,
			Title:  "Service temporarily unavailable",
			Detail: "the command was not confirmed; check the vehicle's state before retrying",
		}
	}
	for _, m := range domainErrors {
		if errors.Is(err, m.err) {
			return problem{
				Status: m.status,
				Code:   m.code,
				Title:  m.title,
				Detail: err.Error(),
			}
		}
	}
	return problem{
		Status: http.StatusInternalServerError,
		Code:   "INTERNAL",
		Title:  "Internal server error",
	}
}
