package repositories

import (
	"database/sql"
	"time"

	"keyloop-coding-challenge/internal/application/projection"
	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/infrastructure/viewstore/models"
)

// Mapping between the read_model rows and the application models. Zero dates, times
// and strings of the application models are NULL in the database; timestamps are
// returned in UTC.

func toInventoryVehicle(v projection.ProjectedVehicle) models.InventoryVehicle {
	return models.InventoryVehicle{
		VehicleID:        v.VehicleID,
		DealershipID:     v.DealershipID,
		VIN:              v.VIN,
		Make:             v.Make,
		Model:            v.Model,
		ModelYear:        v.ModelYear,
		StockedOn:        toModelDate(v.StockedOn),
		Status:           string(v.Status),
		SoldOn:           toNullDate(v.SoldOn),
		ActionID:         toNullString(v.ActionID),
		ActionType:       toNullString(string(v.ActionType)),
		ActionNote:       toNullString(v.ActionNote),
		ActionProposedAt: toNullTime(v.ActionProposedAt),
		ActionUpdatedAt:  toNullTime(v.ActionUpdatedAt),
		ActionUpdatedBy:  toNullString(v.ActionUpdatedBy),
		Version:          v.Version,
		LastEventID:      v.LastEventID,
		UpdatedAt:        v.UpdatedAt,
	}
}

func toProjectedVehicle(m models.InventoryVehicle) projection.ProjectedVehicle {
	return projection.ProjectedVehicle{
		VehicleID:        m.VehicleID,
		DealershipID:     m.DealershipID,
		VIN:              m.VIN,
		Make:             m.Make,
		Model:            m.Model,
		ModelYear:        m.ModelYear,
		StockedOn:        toDomainDate(m.StockedOn),
		Status:           domain.InventoryStatus(m.Status),
		SoldOn:           fromNullDate(m.SoldOn),
		ActionID:         m.ActionID.String,
		ActionType:       domain.ActionType(m.ActionType.String),
		ActionNote:       m.ActionNote.String,
		ActionProposedAt: fromNullTime(m.ActionProposedAt),
		ActionUpdatedAt:  fromNullTime(m.ActionUpdatedAt),
		ActionUpdatedBy:  m.ActionUpdatedBy.String,
		Version:          m.Version,
		LastEventID:      m.LastEventID,
		UpdatedAt:        m.UpdatedAt.UTC(),
	}
}

func toQueryVehicle(m models.InventoryVehicle) query.Vehicle {
	v := query.Vehicle{
		VehicleID:    m.VehicleID,
		DealershipID: m.DealershipID,
		VIN:          m.VIN,
		Make:         m.Make,
		Model:        m.Model,
		ModelYear:    m.ModelYear,
		StockedOn:    toDomainDate(m.StockedOn),
		Status:       domain.InventoryStatus(m.Status),
		SoldOn:       fromNullDate(m.SoldOn),
		Version:      m.Version,
		UpdatedAt:    m.UpdatedAt.UTC(),
	}
	if m.ActionID.Valid {
		v.Action = &query.Action{
			ID:         m.ActionID.String,
			Type:       domain.ActionType(m.ActionType.String),
			Note:       m.ActionNote.String,
			ProposedAt: fromNullTime(m.ActionProposedAt),
			UpdatedAt:  fromNullTime(m.ActionUpdatedAt),
			UpdatedBy:  m.ActionUpdatedBy.String,
		}
	}
	return v
}

func toActionHistoryRow(e projection.ActionHistoryEntry) models.VehicleActionHistory {
	return models.VehicleActionHistory{
		VehicleID:     e.VehicleID,
		Version:       e.Version,
		EventID:       e.EventID,
		ActionID:      e.ActionID,
		Change:        e.Change,
		ActionType:    string(e.ActionType),
		Note:          e.Note,
		Reason:        e.Reason,
		ActorID:       e.ActorID,
		CorrelationID: e.CorrelationID,
		OccurredAt:    e.OccurredAt,
	}
}

func toQueryHistoryEntry(m models.VehicleActionHistory) query.HistoryEntry {
	return query.HistoryEntry{
		Version:       m.Version,
		EventID:       m.EventID,
		ActionID:      m.ActionID,
		Change:        m.Change,
		ActionType:    domain.ActionType(m.ActionType),
		Note:          m.Note,
		Reason:        m.Reason,
		ActorID:       m.ActorID,
		CorrelationID: m.CorrelationID,
		OccurredAt:    m.OccurredAt.UTC(),
	}
}

func toModelDate(d domain.Date) models.Date {
	if d.IsZero() {
		return models.Date{}
	}
	// domain.Date exposes its calendar fields only through String.
	t, err := time.Parse("2006-01-02", d.String())
	if err != nil {
		panic("domain.Date.String is not YYYY-MM-DD: " + err.Error())
	}
	return models.Date{
		Year:  t.Year(),
		Month: t.Month(),
		Day:   t.Day(),
	}
}

func toDomainDate(d models.Date) domain.Date {
	return domain.NewDate(d.Year, d.Month, d.Day)
}

func toNullDate(d domain.Date) models.NullDate {
	if d.IsZero() {
		return models.NullDate{}
	}
	return models.NullDate{
		Date:  toModelDate(d),
		Valid: true,
	}
}

func fromNullDate(d models.NullDate) domain.Date {
	if !d.Valid {
		return domain.Date{}
	}
	return toDomainDate(d.Date)
}

func toNullString(s string) sql.NullString {
	return sql.NullString{
		String: s,
		Valid:  s != "",
	}
}

func toNullTime(t time.Time) sql.NullTime {
	return sql.NullTime{
		Time:  t,
		Valid: !t.IsZero(),
	}
}

func fromNullTime(t sql.NullTime) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}
