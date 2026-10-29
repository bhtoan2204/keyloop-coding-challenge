package query

import (
	"time"

	"keyloop-coding-challenge/internal/domain"
)

// History change kinds.
const (
	ChangeProposed     = "PROPOSED"
	ChangeRevised      = "REVISED"
	ChangeCancelled    = "CANCELLED"
	ChangeClosedBySale = "CLOSED_BY_SALE"
)

type HistoryEntry struct {
	Version       int
	EventID       int64
	ActionID      string
	Change        string
	ActionType    domain.ActionType
	Note          string
	Reason        string
	ActorID       string
	CorrelationID string
	OccurredAt    time.Time
}
