package messaging

import "errors"

// Errors that retrying the same record cannot fix. The consumer never skips such a
// record: it halts the record's partition (retrying with backoff, offset not
// committed) and reports not ready until an operator has fixed the cause.
var (
	// ErrMalformedRecord: the record is not a Debezium change event of an es_event row
	// (empty value, invalid JSON, missing columns, key and row disagree).
	ErrMalformedRecord = errors.New("malformed change record")
	// ErrUnexpectedOperation: es_event is append-only, so an update, delete or truncate
	// in its change feed means history was rewritten outside the application.
	ErrUnexpectedOperation = errors.New("unexpected change operation on the append-only event table")
	// ErrUnknownEventType: no event type is registered for the row's aggregate and
	// event type (a newer writer, or a missing registration).
	ErrUnknownEventType = errors.New("unknown event type")
	// ErrUnsupportedSchemaVersion: the payload schema is newer than this build.
	ErrUnsupportedSchemaVersion = errors.New("unsupported event schema version")
	// ErrRejectedEvent: the projection refused the event (it does not fit the projected
	// state, or the projector does not support it).
	ErrRejectedEvent = errors.New("event rejected by the projection")
	// ErrUnrepairableGap: versions missing from the change feed are missing from the
	// event store too, so they cannot be applied.
	ErrUnrepairableGap = errors.New("version gap cannot be repaired from the event store")
)

// ErrVersionGap is returned by the Projector port when an event is not the next
// version of its aggregate. The consumer repairs it from the event store.
var ErrVersionGap = errors.New("aggregate version gap")

// ErrTransportClosed is returned by Transport.Poll once the transport is closed.
var ErrTransportClosed = errors.New("change feed transport closed")

// ErrFeedDataLoss is returned (joined with other poll errors) by Transport.Poll when
// the transport had to move a partition forward past records it never delivered: they
// were deleted by retention before being consumed, or the log was truncated. The
// consumer re-syncs from the event store, so the skipped records are applied anyway.
var ErrFeedDataLoss = errors.New("change feed skipped records that were never consumed")

// isPoison reports whether err needs operator action: retrying cannot succeed. Any
// other error (database or broker unavailable, timeouts) is treated as transient.
// Both kinds are retried in place and never skipped; the kind only decides the log
// level and how quickly the retry backs off.
func isPoison(err error) bool {
	for _, target := range []error{
		ErrMalformedRecord,
		ErrUnexpectedOperation,
		ErrUnknownEventType,
		ErrUnsupportedSchemaVersion,
		ErrRejectedEvent,
		ErrUnrepairableGap,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
