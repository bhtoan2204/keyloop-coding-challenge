package repos

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

var (
	ErrConcurrencyConflict   = errors.New("optimistic concurrency conflict")
	ErrAggregateNotFound     = errors.New("aggregate not found")
	ErrAggregateTypeMismatch = errors.New("aggregate type mismatch")
	ErrDuplicateEvent        = errors.New("duplicate event version")

	// ErrUnsupportedSchemaVersion means a stored payload has a schema version this code
	// cannot decode (e.g. written by a newer release). Decoding it with the current
	// struct could silently drop or misread fields, so it is refused.
	ErrUnsupportedSchemaVersion = errors.New("unsupported event schema version")

	// ErrUnknownEventType means a stored event's aggregate/event type is not registered
	// with the serializer (written by a newer release, or a missing registration).
	ErrUnknownEventType = errors.New("unknown event type")

	// ErrRetryable marks a transient database failure (deadlock, serialization failure,
	// lock not available). The transaction was rolled back; the caller may reload the
	// aggregate and re-run the command. It is not an optimistic concurrency conflict.
	ErrRetryable = errors.New("retryable database error")
)

// PostgreSQL SQLSTATE codes handled explicitly.
const (
	sqlstateUniqueViolation      = "23505"
	sqlstateSerializationFailure = "40001"
	sqlstateDeadlockDetected     = "40P01"
	sqlstateLockNotAvailable     = "55P03"
	sqlstateQueryCanceled        = "57014"
	sqlstateAdminShutdown        = "57P01"
	sqlstateCrashShutdown        = "57P02"
	sqlstateCannotConnectNow     = "57P03"
	sqlstateClassConnection      = "08"
)

func pgCode(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code, true
	}
	return "", false
}

// isDuplicateKey reports a unique violation. Callers decide what it means in their
// context (a duplicate event version is not by itself a concurrency conflict).
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	if code, ok := pgCode(err); ok {
		return code == sqlstateUniqueViolation
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	// SQLite (tests) has no typed error here without importing the cgo driver.
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}

// classifyTxError marks deadlocks, serialization failures and lock-not-available as
// ErrRetryable: PostgreSQL rolled the transaction back, so nothing was committed and a
// reload-and-retry is safe. Every other error is returned unchanged.
func classifyTxError(err error) error {
	code, ok := pgCode(err)
	if !ok {
		return err
	}
	switch code {
	case sqlstateSerializationFailure, sqlstateDeadlockDetected, sqlstateLockNotAvailable:
		return fmt.Errorf("%w: %w", ErrRetryable, err)
	}
	return err
}

// IsUnavailable reports errors that mean the database could not be reached, the
// connection broke, the server is shutting down or a statement was cancelled. Around
// COMMIT the outcome is then unknown, so callers must not retry automatically.
func IsUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	var connectErr *pgconn.ConnectError
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &netErr) || errors.As(err, &connectErr) {
		return true
	}
	code, ok := pgCode(err)
	if !ok {
		return false
	}
	switch code {
	case sqlstateQueryCanceled, sqlstateAdminShutdown, sqlstateCrashShutdown, sqlstateCannotConnectNow:
		return true
	}
	return strings.HasPrefix(code, sqlstateClassConnection)
}
