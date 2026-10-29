package presentation

import (
	"net/http"
	"strconv"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/domain"
)

type stockVehicleRequest struct {
	VehicleID string `json:"vehicle_id"`
	VIN       string `json:"vin"`
	Make      string `json:"make"`
	Model     string `json:"model"`
	ModelYear int    `json:"model_year"`
	StockedOn string `json:"stocked_on"`
}

type actionRequest struct {
	ActionType string `json:"action_type"`
	Note       string `json:"note"`
}

type cancelActionRequest struct {
	Reason string `json:"reason"`
}

type sellVehicleRequest struct {
	SoldOn string `json:"sold_on"`
}

// vehicleResponse is the committed write-side state after a command. age_days and
// is_aging are derived at the dealership business date (business_date) and are not
// stored anywhere.
type vehicleResponse struct {
	VehicleID     string          `json:"vehicle_id"`
	DealershipID  string          `json:"dealership_id"`
	VIN           string          `json:"vin"`
	Make          string          `json:"make"`
	Model         string          `json:"model"`
	ModelYear     int             `json:"model_year"`
	StockedOn     string          `json:"stocked_on"`
	Status        string          `json:"status"`
	SoldOn        *string         `json:"sold_on"`
	AgeDays       int             `json:"age_days"`
	IsAging       bool            `json:"is_aging"`
	CurrentAction *actionResponse `json:"current_action"`
	Version       int             `json:"version"`
	BusinessDate  string          `json:"business_date"`
}

type actionResponse struct {
	ActionID   string `json:"action_id"`
	ActionType string `json:"action_type"`
	Note       string `json:"note"`
}

func toVehicleResponse(res *command.Result) vehicleResponse {
	v := res.Vehicle
	out := vehicleResponse{
		VehicleID: v.VehicleID(), DealershipID: v.DealershipID(), VIN: v.VIN(), Make: v.Make(), Model: v.Model(),
		ModelYear: v.ModelYear(), StockedOn: v.StockedOn().String(), Status: string(v.Status()),
		AgeDays: v.AgeInDays(res.BusinessDate), IsAging: v.IsAging(res.BusinessDate),
		Version: v.Version(), BusinessDate: res.BusinessDate.String(),
	}
	if !v.SoldOn().IsZero() {
		s := v.SoldOn().String()
		out.SoldOn = &s
	}
	if a, ok := v.ActiveAction(); ok {
		out.CurrentAction = &actionResponse{ActionID: a.ID, ActionType: string(a.Type), Note: a.Note}
	}
	return out
}

// writeVehicle answers with the committed state; the ETag is the aggregate version.
func writeVehicle(w http.ResponseWriter, status int, res *command.Result) {
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(res.Vehicle.Version())))
	writeJSON(w, status, "application/json", toVehicleResponse(res))
}

func parseDate(field, value string) (domain.Date, error) {
	if value == "" {
		return domain.Date{}, invalidField(field, "is required")
	}
	d, err := domain.ParseDate(value)
	if err != nil {
		return domain.Date{}, invalidField(field, "must be a date in YYYY-MM-DD format")
	}
	return d, nil
}

// target reads the vehicle address from the URL and If-Match; the dealership always
// comes from the URL, never from the body.
func target(r *http.Request) (command.Target, error) {
	expected, err := expectedVersion(r)
	if err != nil {
		return command.Target{}, err
	}
	return command.Target{
		DealershipID: r.PathValue("dealershipId"), VehicleID: r.PathValue("vehicleId"), ExpectedVersion: expected,
	}, nil
}

func (a *api) stockVehicle(w http.ResponseWriter, r *http.Request) {
	meta, err := commandMeta(r)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	var req stockVehicleRequest
	if err := decodeJSON(w, r, &req, false); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	if req.VehicleID != "" && !safeID.MatchString(req.VehicleID) {
		writeError(w, r, a.log, invalidField("vehicle_id", "must be 1-128 characters of A-Z a-z 0-9 . _ : @ -"))
		return
	}
	stockedOn, err := parseDate("stocked_on", req.StockedOn)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.commands.StockVehicle(r.Context(), command.StockVehicle{
		DealershipID: r.PathValue("dealershipId"), VehicleID: req.VehicleID, VIN: req.VIN, Make: req.Make,
		Model: req.Model, ModelYear: req.ModelYear, StockedOn: stockedOn,
	}, meta)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	w.Header().Set("Location", "/v1/dealerships/"+res.Vehicle.DealershipID()+"/vehicles/"+res.Vehicle.VehicleID())
	writeVehicle(w, http.StatusCreated, res)
}

func (a *api) proposeAction(w http.ResponseWriter, r *http.Request) {
	meta, t, err := commandInputs(r)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	var req actionRequest
	if err := decodeJSON(w, r, &req, false); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.commands.ProposeAction(r.Context(), command.ProposeAction{
		Target: t, ActionType: domain.ActionType(req.ActionType), Note: req.Note,
	}, meta)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	w.Header().Set("Location", r.URL.Path+"/"+res.ActionID)
	writeVehicle(w, http.StatusCreated, res)
}

func (a *api) reviseAction(w http.ResponseWriter, r *http.Request) {
	meta, t, err := commandInputs(r)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	var req actionRequest
	if err := decodeJSON(w, r, &req, false); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.commands.ReviseAction(r.Context(), command.ReviseAction{
		Target: t, ActionID: r.PathValue("actionId"), ActionType: domain.ActionType(req.ActionType), Note: req.Note,
	}, meta)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeVehicle(w, http.StatusOK, res)
}

func (a *api) cancelAction(w http.ResponseWriter, r *http.Request) {
	meta, t, err := commandInputs(r)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	var req cancelActionRequest
	if err := decodeJSON(w, r, &req, true); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.commands.CancelAction(r.Context(), command.CancelAction{
		Target: t, ActionID: r.PathValue("actionId"), Reason: req.Reason,
	}, meta)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeVehicle(w, http.StatusOK, res)
}

func (a *api) sellVehicle(w http.ResponseWriter, r *http.Request) {
	meta, t, err := commandInputs(r)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	var req sellVehicleRequest
	if err := decodeJSON(w, r, &req, false); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	soldOn, err := parseDate("sold_on", req.SoldOn)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.commands.SellVehicle(r.Context(), command.SellVehicle{Target: t, SoldOn: soldOn}, meta)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeVehicle(w, http.StatusOK, res)
}

func commandInputs(r *http.Request) (command.Meta, command.Target, error) {
	meta, err := commandMeta(r)
	if err != nil {
		return command.Meta{}, command.Target{}, err
	}
	t, err := target(r)
	return meta, t, err
}
