package aggregates

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"keyloop-coding-challenge/internal/domain"
)

const (
	maxNameLen   = 64
	maxNoteLen   = 1000
	minModelYear = 1900
)

// VINs are 17 characters and never contain I, O or Q.
var vinPattern = regexp.MustCompile(`^[A-HJ-NPR-Z0-9]{17}$`)

func requiredName(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", domain.Invalid(field, "is required")
	}
	if utf8.RuneCountInString(value) > maxNameLen {
		return "", domain.Invalid(field, fmt.Sprintf("must be at most %d characters", maxNameLen))
	}
	return value, nil
}

func validateAction(t domain.ActionType, note string) (string, error) {
	if !t.Valid() {
		return "", domain.Invalid("action_type", fmt.Sprintf("unknown action type %q", t))
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > maxNoteLen {
		return "", domain.Invalid("note", fmt.Sprintf("must be at most %d characters", maxNoteLen))
	}
	if t == domain.ActionOther && note == "" {
		return "", domain.Invalid("note", "is required when action_type is OTHER")
	}
	return note, nil
}
