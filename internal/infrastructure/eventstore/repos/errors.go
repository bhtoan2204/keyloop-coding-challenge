package repos

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/go-sql-driver/mysql"
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

	// ErrRetryable marks a transient database failure (deadlock, lock wait timeout).
	// The transaction was rolled back; the caller may reload the aggregate and re-run
	// the command. It is not an optimistic concurrency conflict.
	ErrRetryable = errors.New("retryable database error")
)

// MySQL server error numbers handled explicitly.
const (
	mysqlDuplicateEntry  = 1062
	mysqlLockWaitTimeout = 1205
	mysqlDeadlock        = 1213
)

func mysqlErrorNumber(err error) (uint16, bool) {
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		return myErr.Number, true
	}
	return 0, false
}

func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	if n, ok := mysqlErrorNumber(err); ok {
		return n == mysqlDuplicateEntry
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	// SQLite (tests) has no typed error here without importing the cgo driver.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "duplicate key")
}

// classifyTxError marks deadlocks and lock wait timeouts as ErrRetryable and leaves
// every other error (conflicts, constraint violations, connection and context errors)
// unchanged, so callers can still match them with errors.Is/As.
func classifyTxError(err error) error {
	if n, ok := mysqlErrorNumber(err); ok && (n == mysqlDeadlock || n == mysqlLockWaitTimeout) {
		return fmt.Errorf("%w: %w", ErrRetryable, err)
	}
	return err
}

// IsUnavailable reports errors that mean the database could not be reached or the
// connection broke mid-request. When it happens around COMMIT the outcome is unknown,
// so callers must not retry automatically.
func IsUnavailable(err error) bool {
	var netErr net.Error
	return errors.Is(err, driver.ErrBadConn) || errors.Is(err, mysql.ErrInvalidConn) ||
		errors.Is(err, sql.ErrConnDone) || errors.As(err, &netErr)
}
