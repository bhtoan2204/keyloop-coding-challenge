package presentation

import (
	"net/http"
	"strconv"

	"keyloop-coding-challenge/internal/application/command"
)

// toVehicleResponse maps the committed aggregate state. age_days and is_aging are
// derived at the business date and never stored.
func toVehicleResponse(res *command.Result) vehicleResponse {
	v := res.Vehicle
	out := vehicleResponse{
		VehicleID:    v.VehicleID(),
		DealershipID: v.DealershipID(),
		VIN:          v.VIN(),
		Make:         v.Make(),
		Model:        v.Model(),
		ModelYear:    v.ModelYear(),
		StockedOn:    v.StockedOn().String(),
		Status:       string(v.Status()),
		AgeDays:      v.AgeInDays(res.BusinessDate),
		IsAging:      v.IsAging(res.BusinessDate),
		Version:      v.Version(),
		BusinessDate: res.BusinessDate.String(),
	}
	if !v.SoldOn().IsZero() {
		soldOn := v.SoldOn().String()
		out.SoldOn = &soldOn
	}
	if a, ok := v.ActiveAction(); ok {
		out.CurrentAction = &actionResponse{
			ActionID:   a.ID,
			ActionType: string(a.Type),
			Note:       a.Note,
		}
	}
	return out
}

// writeVehicle answers with the committed state; the ETag is the aggregate version.
func writeVehicle(w http.ResponseWriter, status int, res *command.Result) {
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(res.Vehicle.Version())))
	writeJSON(w, status, "application/json", toVehicleResponse(res))
}
