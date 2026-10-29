package queries

import (
	"net/http"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/presentation/httpkit"
)

func (h *handlers) listInventory(w http.ResponseWriter, r *http.Request) {
	q, err := parseListInventory(r)
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	page, err := h.service.ListInventory(r.Context(), q)
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, toInventoryListResponse(page))
}

// parseListInventory reads the list filters. Parameters are parsed in a fixed order so
// the first invalid one is the one reported.
func parseListInventory(r *http.Request) (query.ListInventory, error) {
	params, err := httpkit.QueryParams(r, "make", "model", "min_age_days", "max_age_days", "aging", "status", "sort", "limit", "cursor", "as_of")
	if err != nil {
		return query.ListInventory{}, err
	}
	q := query.ListInventory{
		DealershipID: r.PathValue("dealershipId"),
		Make:         params.Get("make"),
		Model:        params.Get("model"),
		Status:       params.Get("status"),
		Sort:         params.Get("sort"),
		Cursor:       params.Get("cursor"),
	}
	if q.MinAgeDays, err = httpkit.OptionalInt(params, "min_age_days"); err != nil {
		return q, err
	}
	if q.MaxAgeDays, err = httpkit.OptionalInt(params, "max_age_days"); err != nil {
		return q, err
	}
	if q.Limit, err = httpkit.IntOrZero(params, "limit"); err != nil {
		return q, err
	}
	if q.AgingOnly, err = httpkit.OptionalBool(params, "aging"); err != nil {
		return q, err
	}
	if q.AsOf, err = httpkit.OptionalDate(params, "as_of"); err != nil {
		return q, err
	}
	return q, nil
}
