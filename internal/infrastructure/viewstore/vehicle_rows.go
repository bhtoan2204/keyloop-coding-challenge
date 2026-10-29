package viewstore

import (
	"database/sql"
	"time"

	"keyloop-coding-challenge/internal/domain"
)

const vehicleColumns = `vehicle_id, dealership_id, vin, make, model, model_year, stocked_on, status, sold_on,
	action_id, action_type, action_note, action_proposed_at, action_updated_at, action_updated_by,
	version, last_event_id, updated_at`

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanVehicle(s scanner) (VehicleRow, error) {
	var (
		r                                     VehicleRow
		status                                string
		stockedOn                             time.Time
		soldOn, proposedAt, updatedAt         sql.NullTime
		actionID, actionType, note, updatedBy sql.NullString
	)
	err := s.Scan(&r.VehicleID, &r.DealershipID, &r.VIN, &r.Make, &r.Model, &r.ModelYear, &stockedOn, &status, &soldOn,
		&actionID, &actionType, &note, &proposedAt, &updatedAt, &updatedBy, &r.Version, &r.LastEventID, &r.UpdatedAt)
	if err != nil {
		return r, err
	}
	r.StockedOn = dateOf(stockedOn)
	r.Status = domain.InventoryStatus(status)
	if soldOn.Valid {
		r.SoldOn = dateOf(soldOn.Time)
	}
	r.ActionID, r.ActionType, r.ActionNote, r.ActionUpdatedBy = actionID.String, domain.ActionType(actionType.String), note.String, updatedBy.String
	if proposedAt.Valid {
		r.ActionProposedAt = proposedAt.Time.UTC()
	}
	if updatedAt.Valid {
		r.ActionUpdatedAt = updatedAt.Time.UTC()
	}
	r.UpdatedAt = r.UpdatedAt.UTC()
	return r, nil
}

func dateOf(t time.Time) domain.Date {
	return domain.NewDate(t.Year(), t.Month(), t.Day())
}

func nullDate(d domain.Date) interface{} {
	if d.IsZero() {
		return nil
	}
	return d.String()
}

func nullTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
