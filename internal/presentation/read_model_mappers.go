package presentation

import (
	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
)

func toInventoryListResponse(page *query.InventoryPage) inventoryListResponse {
	out := inventoryListResponse{
		AsOf:               page.AsOf.String(),
		AgingThresholdDays: domain.AgingThresholdDays,
		Data:               []inventoryItem{},
		Page:               toPage(page.Limit, page.HasMore, page.NextCursor),
	}
	for _, v := range page.Vehicles {
		out.Data = append(out.Data, toInventoryItem(v, page.AsOf))
	}
	return out
}

func toVehicleDetailResponse(res *query.VehicleResult) vehicleDetailResponse {
	return vehicleDetailResponse{
		inventoryItem: toInventoryItem(res.Vehicle, res.AsOf),
		AsOf:          res.AsOf.String(),
	}
}

// toInventoryItem derives age_days and is_aging at asOf; neither is stored.
func toInventoryItem(v query.Vehicle, asOf domain.Date) inventoryItem {
	item := inventoryItem{
		VehicleID:    v.VehicleID,
		DealershipID: v.DealershipID,
		VIN:          v.VIN,
		Make:         v.Make,
		Model:        v.Model,
		ModelYear:    v.ModelYear,
		StockedOn:    v.StockedOn.String(),
		Status:       string(v.Status),
		AgeDays:      v.AgeDays(asOf),
		IsAging:      v.IsAging(asOf),
		Version:      v.Version,
	}
	if !v.SoldOn.IsZero() {
		soldOn := v.SoldOn.String()
		item.SoldOn = &soldOn
	}
	if a := v.Action; a != nil {
		item.CurrentAction = &currentActionDetail{
			ActionID:   a.ID,
			ActionType: string(a.Type),
			Note:       a.Note,
			ProposedAt: a.ProposedAt,
			UpdatedAt:  a.UpdatedAt,
			UpdatedBy:  a.UpdatedBy,
		}
	}
	return item
}

func toHistoryResponse(page *query.HistoryPage) historyResponse {
	out := historyResponse{
		Data: []historyItem{},
		Page: toPage(page.Limit, page.HasMore, page.NextCursor),
	}
	for _, h := range page.Entries {
		out.Data = append(out.Data, historyItem{
			EventID:       h.EventID,
			Version:       h.Version,
			ActionID:      h.ActionID,
			Change:        h.Change,
			ActionType:    string(h.ActionType),
			Note:          h.Note,
			Reason:        h.Reason,
			ActorID:       h.ActorID,
			CorrelationID: h.CorrelationID,
			OccurredAt:    h.OccurredAt,
		})
	}
	return out
}

func toSummaryResponse(res *query.SummaryResult) summaryResponse {
	out := summaryResponse{
		AsOf:               res.AsOf.String(),
		AgingThresholdDays: domain.AgingThresholdDays,
		InStock:            res.InStock,
		Aging:              res.Aging,
		AgingWithoutAction: res.AgingWithoutAction,
	}
	if !res.OldestStockedOn.IsZero() {
		oldest := res.OldestStockedOn.String()
		out.OldestStockedOn = &oldest
	}
	return out
}

func toPage(limit int, hasMore bool, next string) pageInfo {
	p := pageInfo{
		Limit:   limit,
		HasMore: hasMore,
	}
	if next != "" {
		p.NextCursor = &next
	}
	return p
}
