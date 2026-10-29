package viewstore

import "keyloop-coding-challenge/internal/application/query"

var _ query.ReadModel = (*Postgres)(nil)

func toQueryVehicle(r VehicleRow) query.Vehicle {
	v := query.Vehicle{
		VehicleID:    r.VehicleID,
		DealershipID: r.DealershipID,
		VIN:          r.VIN,
		Make:         r.Make,
		Model:        r.Model,
		ModelYear:    r.ModelYear,
		StockedOn:    r.StockedOn,
		Status:       r.Status,
		SoldOn:       r.SoldOn,
		Version:      r.Version,
		UpdatedAt:    r.UpdatedAt,
	}
	if r.ActionID != "" {
		v.Action = &query.Action{
			ID:         r.ActionID,
			Type:       r.ActionType,
			Note:       r.ActionNote,
			ProposedAt: r.ActionProposedAt,
			UpdatedAt:  r.ActionUpdatedAt,
			UpdatedBy:  r.ActionUpdatedBy,
		}
	}
	return v
}
