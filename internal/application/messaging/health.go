package messaging

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Consumer phases.
const (
	PhaseStarting  = "starting"
	PhaseSyncing   = "syncing"
	PhaseConsuming = "consuming"
	PhaseStopping  = "stopping"
	PhaseStopped   = "stopped"
)

// Status is the consumer's observable state (served by the projector's /status).
type Status struct {
	Phase             string `json:"phase"`
	Ready             bool   `json:"ready"`
	EventsApplied     int64  `json:"events_applied"`
	DuplicatesSkipped int64  `json:"duplicates_skipped"`
	GapEventsRepaired int64  `json:"gap_events_repaired"`
	// RecordsFailed counts failed attempts (each retry of a halted record counts).
	RecordsFailed int64 `json:"records_failed"`
	// LastAppliedAt is when the last event was applied (not skipped).
	LastAppliedAt *time.Time `json:"last_applied_at,omitempty"`
	// LastChangeLagMs is apply time minus the MySQL commit time of the last live
	// (binlog, not snapshot) record applied, as reported by Debezium. Approximate: the
	// two clocks belong to different hosts.
	LastChangeLagMs *int64            `json:"last_change_lag_ms,omitempty"`
	TransportError  string            `json:"transport_error,omitempty"`
	Partitions      []PartitionStatus `json:"partitions"`
}

// PartitionStatus is the progress of one partition consumed by this instance.
type PartitionStatus struct {
	Topic     string `json:"topic"`
	Partition int32  `json:"partition"`
	// LastOffset is the last record processed (applied or skipped as a duplicate).
	LastOffset int64 `json:"last_offset"`
	// Failing is true while the record at FailingOffset keeps failing. The partition
	// does not move past it.
	Failing       bool   `json:"failing"`
	Poison        bool   `json:"poison,omitempty"`
	FailingOffset int64  `json:"failing_offset,omitempty"`
	Failures      int    `json:"consecutive_failures,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

// health is the consumer's shared state: partition workers, the poll loop and
// rebalance callbacks update it concurrently.
type health struct {
	mu         sync.Mutex
	status     Status
	partitions map[partitionID]*PartitionStatus
	// owned is the assignment reported by the transport; nil until the first report
	// (then every partition counts as owned). A batch still running when its
	// partition is lost must not re-create that partition's entry afterwards.
	owned map[partitionID]bool
}

func newHealth() *health {
	return &health{
		status:     Status{Phase: PhaseStarting},
		partitions: make(map[partitionID]*PartitionStatus),
	}
}

// setPhase moves to phase; once shutdown has begun only "stopped" may follow, so a
// late "consuming" cannot make a draining consumer look ready.
func (h *health) setPhase(phase string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status.Phase == PhaseStopping && phase != PhaseStopped || h.status.Phase == PhaseStopped {
		return
	}
	h.status.Phase = phase
}

func (h *health) recordRepaired(repaired int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status.GapEventsRepaired += int64(repaired)
}

func (h *health) recordEvent(changed ChangeEvent, applied bool, repaired int, at time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status.GapEventsRepaired += int64(repaired)
	if !applied {
		h.status.DuplicatesSkipped++
		return
	}
	h.status.EventsApplied++
	h.status.LastAppliedAt = &at
	if !changed.Snapshot && !changed.ChangedAt.IsZero() {
		lag := at.Sub(changed.ChangedAt).Milliseconds()
		h.status.LastChangeLagMs = &lag
	}
}

func (h *health) partition(id partitionID) *PartitionStatus {
	p, ok := h.partitions[id]
	if !ok {
		p = &PartitionStatus{
			Topic:      id.topic,
			Partition:  id.partition,
			LastOffset: -1,
		}
		h.partitions[id] = p
	}
	return p
}

func (h *health) isOwned(id partitionID) bool {
	return h.owned == nil || h.owned[id]
}

func (h *health) partitionProcessed(rec Record) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.isOwned(rec.partitionID()) {
		return
	}
	p := h.partition(rec.partitionID())
	p.LastOffset = rec.Offset
	p.Failing = false
	p.Poison = false
	p.FailingOffset = 0
	p.Failures = 0
	p.LastError = ""
}

// partitionFailed records a failure of rec and returns the consecutive failure count.
func (h *health) partitionFailed(rec Record, err error, poison bool) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.isOwned(rec.partitionID()) {
		return 1
	}
	h.status.RecordsFailed++
	p := h.partition(rec.partitionID())
	if !p.Failing || p.FailingOffset != rec.Offset {
		p.Failures = 0
	}
	p.Failing = true
	p.Poison = poison
	p.FailingOffset = rec.Offset
	p.Failures++
	p.LastError = err.Error()
	return p.Failures
}

// forget drops partitions this instance no longer owns (revoked or lost): another
// member resumes them from the committed offset and reports their health.
func (h *health) forget(topic string, partitions []int32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.owned == nil {
		h.owned = make(map[partitionID]bool)
	}
	for _, p := range partitions {
		id := partitionID{topic: topic, partition: p}
		delete(h.partitions, id)
		delete(h.owned, id)
	}
}

func (h *health) assign(topic string, partitions []int32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.owned == nil {
		h.owned = make(map[partitionID]bool)
	}
	for _, p := range partitions {
		h.owned[partitionID{topic: topic, partition: p}] = true
	}
}

func (h *health) transportResult(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status.TransportError = ""
	if err != nil {
		h.status.TransportError = err.Error()
	}
}

func (h *health) snapshot() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.status
	s.Partitions = make([]PartitionStatus, 0, len(h.partitions))
	for _, p := range h.partitions {
		s.Partitions = append(s.Partitions, *p)
	}
	sort.Slice(s.Partitions, func(i, j int) bool {
		a, b := s.Partitions[i], s.Partitions[j]
		if a.Topic != b.Topic {
			return a.Topic < b.Topic
		}
		return a.Partition < b.Partition
	})
	s.Ready = readiness(s) == nil
	return s
}

// readiness is nil while the consumer makes progress: bootstrapped, the transport
// healthy and no partition stuck on a failing record.
func readiness(s Status) error {
	if s.Phase != PhaseConsuming {
		return fmt.Errorf("consumer is %s", s.Phase)
	}
	if s.TransportError != "" {
		return fmt.Errorf("change feed transport: %s", s.TransportError)
	}
	var failing []error
	for _, p := range s.Partitions {
		if p.Failing {
			failing = append(failing, fmt.Errorf("%s[%d] halted at offset %d after %d failures: %s",
				p.Topic, p.Partition, p.FailingOffset, p.Failures, p.LastError))
		}
	}
	return errors.Join(failing...)
}
