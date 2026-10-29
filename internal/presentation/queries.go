package presentation

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
)

// QueryService is implemented by *query.Service. Responses come from the PostgreSQL
// read model, which is eventually consistent with the event store (in Phase 4 it is
// refreshed by the sync-projections command, not continuously).
type QueryService interface {
	ListInventory(ctx context.Context, q query.ListInventory) (*query.InventoryPage, error)
	GetVehicle(ctx context.Context, dealershipID, vehicleID string, asOf *domain.Date) (*query.VehicleResult, error)
	GetActionHistory(ctx context.Context, dealershipID, vehicleID string, limit int, cursor string) (*query.HistoryPage, error)
	GetInventorySummary(ctx context.Context, dealershipID string, asOf *domain.Date) (*query.SummaryResult, error)
}

func (a *api) registerQueries(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/dealerships/{dealershipId}/vehicles", a.listInventory)
	mux.HandleFunc("GET /v1/dealerships/{dealershipId}/vehicles/{vehicleId}", a.getVehicle)
	mux.HandleFunc("GET /v1/dealerships/{dealershipId}/vehicles/{vehicleId}/actions", a.getActionHistory)
	mux.HandleFunc("GET /v1/dealerships/{dealershipId}/inventory/summary", a.getInventorySummary)
}

// --- DTOs ---------------------------------------------------------------------------

type inventoryItem struct {
	VehicleID     string               `json:"vehicle_id"`
	DealershipID  string               `json:"dealership_id"`
	VIN           string               `json:"vin"`
	Make          string               `json:"make"`
	Model         string               `json:"model"`
	ModelYear     int                  `json:"model_year"`
	StockedOn     string               `json:"stocked_on"`
	Status        string               `json:"status"`
	SoldOn        *string              `json:"sold_on"`
	AgeDays       int                  `json:"age_days"`
	IsAging       bool                 `json:"is_aging"`
	CurrentAction *currentActionDetail `json:"current_action"`
	Version       int                  `json:"version"`
}

type currentActionDetail struct {
	ActionID   string    `json:"action_id"`
	ActionType string    `json:"action_type"`
	Note       string    `json:"note"`
	ProposedAt time.Time `json:"proposed_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	UpdatedBy  string    `json:"updated_by"`
}

type pageInfo struct {
	Limit      int     `json:"limit"`
	HasMore    bool    `json:"has_more"`
	NextCursor *string `json:"next_cursor"`
}

type inventoryListResponse struct {
	AsOf               string          `json:"as_of"`
	AgingThresholdDays int             `json:"aging_threshold_days"`
	Data               []inventoryItem `json:"data"`
	Page               pageInfo        `json:"page"`
}

type vehicleDetailResponse struct {
	inventoryItem
	AsOf string `json:"as_of"`
}

type historyItem struct {
	EventID       int64     `json:"event_id"`
	Version       int       `json:"version"`
	ActionID      string    `json:"action_id"`
	Change        string    `json:"change"`
	ActionType    string    `json:"action_type"`
	Note          string    `json:"note"`
	Reason        string    `json:"reason"`
	ActorID       string    `json:"actor_id"`
	CorrelationID string    `json:"correlation_id"`
	OccurredAt    time.Time `json:"occurred_at"`
}

type historyResponse struct {
	Data []historyItem `json:"data"`
	Page pageInfo      `json:"page"`
}

type summaryResponse struct {
	AsOf               string  `json:"as_of"`
	AgingThresholdDays int     `json:"aging_threshold_days"`
	InStock            int     `json:"in_stock"`
	Aging              int     `json:"aging"`
	AgingWithoutAction int     `json:"aging_without_action"`
	OldestStockedOn    *string `json:"oldest_stocked_on"`
}

func toInventoryItem(v query.Vehicle, asOf domain.Date) inventoryItem {
	item := inventoryItem{
		VehicleID: v.VehicleID, DealershipID: v.DealershipID, VIN: v.VIN, Make: v.Make, Model: v.Model,
		ModelYear: v.ModelYear, StockedOn: v.StockedOn.String(), Status: string(v.Status),
		AgeDays: v.AgeDays(asOf), IsAging: v.IsAging(asOf), Version: v.Version,
	}
	if !v.SoldOn.IsZero() {
		s := v.SoldOn.String()
		item.SoldOn = &s
	}
	if a := v.Action; a != nil {
		item.CurrentAction = &currentActionDetail{ActionID: a.ID, ActionType: string(a.Type), Note: a.Note,
			ProposedAt: a.ProposedAt, UpdatedAt: a.UpdatedAt, UpdatedBy: a.UpdatedBy}
	}
	return item
}

func toPage(limit int, hasMore bool, next string) pageInfo {
	p := pageInfo{Limit: limit, HasMore: hasMore}
	if next != "" {
		p.NextCursor = &next
	}
	return p
}

// --- handlers -----------------------------------------------------------------------

func (a *api) listInventory(w http.ResponseWriter, r *http.Request) {
	params, err := queryParams(r, "make", "model", "min_age_days", "max_age_days", "aging", "status", "sort", "limit", "cursor", "as_of")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	q := query.ListInventory{
		DealershipID: r.PathValue("dealershipId"), Make: params.Get("make"), Model: params.Get("model"),
		Status: params.Get("status"), Sort: params.Get("sort"), Cursor: params.Get("cursor"),
	}
	if q.MinAgeDays, err = optionalInt(params, "min_age_days"); err == nil {
		if q.MaxAgeDays, err = optionalInt(params, "max_age_days"); err == nil {
			if q.Limit, err = intOrZero(params, "limit"); err == nil {
				if q.AgingOnly, err = optionalBool(params, "aging"); err == nil {
					q.AsOf, err = optionalDate(params, "as_of")
				}
			}
		}
	}
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	page, err := a.queries.ListInventory(r.Context(), q)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	out := inventoryListResponse{AsOf: page.AsOf.String(), AgingThresholdDays: domain.AgingThresholdDays,
		Data: []inventoryItem{}, Page: toPage(page.Limit, page.HasMore, page.NextCursor)}
	for _, v := range page.Vehicles {
		out.Data = append(out.Data, toInventoryItem(v, page.AsOf))
	}
	writeJSON(w, http.StatusOK, "application/json", out)
}

func (a *api) getVehicle(w http.ResponseWriter, r *http.Request) {
	params, err := queryParams(r, "as_of")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	asOf, err := optionalDate(params, "as_of")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.queries.GetVehicle(r.Context(), r.PathValue("dealershipId"), r.PathValue("vehicleId"), asOf)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(res.Vehicle.Version)))
	writeJSON(w, http.StatusOK, "application/json",
		vehicleDetailResponse{inventoryItem: toInventoryItem(res.Vehicle, res.AsOf), AsOf: res.AsOf.String()})
}

func (a *api) getActionHistory(w http.ResponseWriter, r *http.Request) {
	params, err := queryParams(r, "limit", "cursor")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	limit, err := intOrZero(params, "limit")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	page, err := a.queries.GetActionHistory(r.Context(), r.PathValue("dealershipId"), r.PathValue("vehicleId"), limit, params.Get("cursor"))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	out := historyResponse{Data: []historyItem{}, Page: toPage(page.Limit, page.HasMore, page.NextCursor)}
	for _, h := range page.Entries {
		out.Data = append(out.Data, historyItem{EventID: h.EventID, Version: h.Version, ActionID: h.ActionID,
			Change: h.Change, ActionType: string(h.ActionType), Note: h.Note, Reason: h.Reason,
			ActorID: h.ActorID, CorrelationID: h.CorrelationID, OccurredAt: h.OccurredAt})
	}
	writeJSON(w, http.StatusOK, "application/json", out)
}

func (a *api) getInventorySummary(w http.ResponseWriter, r *http.Request) {
	params, err := queryParams(r, "as_of")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	asOf, err := optionalDate(params, "as_of")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.queries.GetInventorySummary(r.Context(), r.PathValue("dealershipId"), asOf)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	out := summaryResponse{AsOf: res.AsOf.String(), AgingThresholdDays: domain.AgingThresholdDays,
		InStock: res.InStock, Aging: res.Aging, AgingWithoutAction: res.AgingWithoutAction}
	if !res.OldestStockedOn.IsZero() {
		s := res.OldestStockedOn.String()
		out.OldestStockedOn = &s
	}
	writeJSON(w, http.StatusOK, "application/json", out)
}

// --- query-string parsing -----------------------------------------------------------

// queryParams rejects unknown or repeated parameters, so a typo (e.g. "agng=true")
// cannot silently return an unfiltered list.
func queryParams(r *http.Request, allowed ...string) (url.Values, error) {
	params := r.URL.Query()
	known := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		known[k] = true
	}
	for k, v := range params {
		if !known[k] {
			return nil, badRequest("INVALID_QUERY", "unknown query parameter "+strconv.Quote(k))
		}
		if len(v) > 1 {
			return nil, badRequest("INVALID_QUERY", "query parameter "+strconv.Quote(k)+" is repeated")
		}
	}
	return params, nil
}

func optionalInt(p url.Values, name string) (*int, error) {
	raw := p.Get(name)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return nil, invalidField(name, "must be an integer")
	}
	return &v, nil
}

func intOrZero(p url.Values, name string) (int, error) {
	v, err := optionalInt(p, name)
	if err != nil || v == nil {
		return 0, err
	}
	return *v, nil
}

func optionalBool(p url.Values, name string) (bool, error) {
	switch p.Get(name) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	}
	return false, invalidField(name, "must be true or false")
}

func optionalDate(p url.Values, name string) (*domain.Date, error) {
	raw := p.Get(name)
	if raw == "" {
		return nil, nil
	}
	d, err := domain.ParseDate(raw)
	if err != nil {
		return nil, invalidField(name, "must be a date in YYYY-MM-DD format")
	}
	return &d, nil
}
