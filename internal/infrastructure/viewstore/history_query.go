package viewstore

import (
	"context"
	"fmt"

	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain"
)

func (p *Postgres) ListHistory(ctx context.Context, vehicleID string, afterVersion, limit int) ([]query.HistoryEntry, error) {
	rows, err := p.db.WithContext(ctx).Raw(`
		SELECT version, event_id, action_id, change, action_type, note, reason, actor_id, correlation_id, occurred_at
		FROM read_model.vehicle_action_history WHERE vehicle_id = ? AND version > ? ORDER BY version LIMIT ?`,
		vehicleID, afterVersion, limit).Rows()
	if err != nil {
		return nil, fmt.Errorf("list history: %w", err)
	}
	defer rows.Close()
	var out []query.HistoryEntry
	for rows.Next() {
		var h query.HistoryEntry
		var actionType string
		if err := rows.Scan(&h.Version, &h.EventID, &h.ActionID, &h.Change, &actionType, &h.Note, &h.Reason,
			&h.ActorID, &h.CorrelationID, &h.OccurredAt); err != nil {
			return nil, err
		}
		h.ActionType = domain.ActionType(actionType)
		h.OccurredAt = h.OccurredAt.UTC()
		out = append(out, h)
	}
	return out, rows.Err()
}
