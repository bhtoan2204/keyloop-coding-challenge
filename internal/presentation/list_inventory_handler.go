package presentation

import (
	"net/http"

	"keyloop-coding-challenge/internal/application/query"
)

func (a *api) listInventory(w http.ResponseWriter, r *http.Request) {
	q, err := parseListInventory(r)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	page, err := a.queries.ListInventory(r.Context(), q)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, "application/json", toInventoryListResponse(page))
}

// parseListInventory reads the list filters. Parameters are parsed in a fixed order so
// the first invalid one is the one reported.
func parseListInventory(r *http.Request) (query.ListInventory, error) {
	params, err := queryParams(r, "make", "model", "min_age_days", "max_age_days", "aging", "status", "sort", "limit", "cursor", "as_of")
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
	if q.MinAgeDays, err = optionalInt(params, "min_age_days"); err != nil {
		return q, err
	}
	if q.MaxAgeDays, err = optionalInt(params, "max_age_days"); err != nil {
		return q, err
	}
	if q.Limit, err = intOrZero(params, "limit"); err != nil {
		return q, err
	}
	if q.AgingOnly, err = optionalBool(params, "aging"); err != nil {
		return q, err
	}
	if q.AsOf, err = optionalDate(params, "as_of"); err != nil {
		return q, err
	}
	return q, nil
}
