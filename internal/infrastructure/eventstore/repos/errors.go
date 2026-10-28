package repos

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

var (
	ErrConcurrencyConflict   = errors.New("optimistic concurrency conflict")
	ErrAggregateNotFound     = errors.New("aggregate not found")
	ErrAggregateTypeMismatch = errors.New("aggregate type mismatch")
	ErrDuplicateEvent        = errors.New("duplicate event version")
)

func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "duplicate key")
}
