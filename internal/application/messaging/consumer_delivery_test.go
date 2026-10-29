package messaging_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"keyloop-coding-challenge/internal/application/messaging"
	"keyloop-coding-challenge/package/eventsourcing"
)

// spread puts each aggregate's stream on one partition (as the aggregate_id key does)
// and returns the event behind every (partition, offset).
func spread(tr *fakeTransport, partitions int32, streams ...[]storedEvent) map[[2]int64]storedEvent {
	at := make(map[[2]int64]storedEvent)
	offsets := make(map[int32]int64)
	for i, s := range streams {
		p := int32(i) % partitions
		for _, e := range s {
			tr.appendEvent(p, e)
			at[[2]int64{int64(p), offsets[p]}] = e
			offsets[p]++
		}
	}
	return at
}

func waitCommitted(t *testing.T, tr *fakeTransport, offsets map[int32]int64) {
	t.Helper()
	eventually(t, fmt.Sprintf("committed offsets %v", offsets), func() bool {
		for p, want := range offsets {
			if tr.committedOffset(p) != want {
				return false
			}
		}
		return true
	})
}

// TestPartitionsAppliedInOrderAndOffsetsCommittedAfterApply: per-aggregate version
// order holds across concurrently processed partitions, and an offset is committed
// only once its event's projection transaction has committed.
func TestPartitionsAppliedInOrderAndOffsetsCommittedAfterApply(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	tr.maxPoll = 2 // several batches per partition
	var streams [][]storedEvent
	for i := 0; i < 6; i++ {
		streams = append(streams, stream(fmt.Sprintf("v%d", i), int64(100*i+1), 4))
	}
	at := spread(tr, 3, streams...)
	tr.beforeCommit = func(processed []messaging.Record) error {
		for _, r := range processed {
			e := at[[2]int64{int64(r.Partition), r.Offset}]
			if got := proj.version(e.aggregate); got < e.version {
				t.Errorf("offset %d of partition %d committed before %s v%d was applied (applied: v%d)",
					r.Offset, r.Partition, e.aggregate, e.version, got)
			}
		}
		return nil
	}
	start(t, tr, proj, nil, nil, testConfig())

	waitCommitted(t, tr, map[int32]int64{0: 8, 1: 8, 2: 8})
	for i := 0; i < 6; i++ {
		var got []string
		for _, h := range proj.history() {
			if h[:len(fmt.Sprintf("v%d:", i))] == fmt.Sprintf("v%d:", i) {
				got = append(got, h)
			}
		}
		want := []string{fmt.Sprintf("v%d:1", i), fmt.Sprintf("v%d:2", i), fmt.Sprintf("v%d:3", i), fmt.Sprintf("v%d:4", i)}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("aggregate v%d applied as %v", i, got)
		}
	}
	if n := len(proj.history()); n != 24 {
		t.Fatalf("%d events applied, want 24", n)
	}
}

// TestDuplicateDeliveryDoesNotDuplicateHistory: the same change delivered twice (a
// connector replay) is applied once.
func TestDuplicateDeliveryDoesNotDuplicateHistory(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	s := stream("v1", 1, 2)
	for _, e := range []storedEvent{s[0], s[1], s[0], s[1]} {
		tr.appendEvent(0, e)
	}
	r := start(t, tr, proj, nil, nil, testConfig())

	waitCommitted(t, tr, map[int32]int64{0: 4})
	if got := proj.history(); !reflect.DeepEqual(got, []string{"v1:1", "v1:2"}) {
		t.Fatalf("history %v", got)
	}
	if st := r.consumer.Status(); st.EventsApplied != 2 || st.DuplicatesSkipped != 2 {
		t.Fatalf("status %+v", st)
	}
}

// TestCrashAfterProjectionCommitBeforeOffsetCommit: the read model committed but the
// offset did not (crash, broker unavailable). The next member re-reads the records
// and skips them by version: no duplicated history, nothing lost.
func TestCrashAfterProjectionCommitBeforeOffsetCommit(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	for _, e := range stream("v1", 1, 3) {
		tr.appendEvent(0, e)
	}
	tr.beforeCommit = func([]messaging.Record) error { return errors.New("broker unavailable") }
	first := start(t, tr, proj, nil, nil, testConfig())
	eventually(t, "projection of v3", func() bool { return proj.version("v1") == 3 })
	first.stop(t)
	if got := tr.committedOffset(0); got != 0 {
		t.Fatalf("offset %d committed although every commit failed", got)
	}

	tr.beforeCommit = nil
	tr.restart()
	second := start(t, tr, proj, nil, nil, testConfig())
	waitCommitted(t, tr, map[int32]int64{0: 3})
	if got := proj.history(); !reflect.DeepEqual(got, []string{"v1:1", "v1:2", "v1:3"}) {
		t.Fatalf("history %v", got)
	}
	if st := second.consumer.Status(); st.DuplicatesSkipped != 3 || st.EventsApplied != 0 {
		t.Fatalf("restarted consumer status %+v", st)
	}
}

// TestCrashBeforeProjectionCommit: the read-model transaction fails (rolled back, or
// the process dies before COMMIT). The offset does not move past the record, and the
// next member applies it exactly once.
func TestCrashBeforeProjectionCommit(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	for _, e := range stream("v1", 1, 3) {
		tr.appendEvent(0, e)
	}
	proj.setFail(func(ev eventsourcing.Event) error {
		if ev.Version == 2 {
			return errors.New("connection reset before COMMIT")
		}
		return nil
	})
	first := start(t, tr, proj, nil, nil, testConfig())
	eventually(t, "v2 retried", func() bool { return len(tr.retried()) >= 2 })
	first.stop(t)
	if got := tr.committedOffset(0); got != 1 {
		t.Fatalf("committed offset %d, want 1 (after v1 only)", got)
	}
	if got := proj.history(); !reflect.DeepEqual(got, []string{"v1:1"}) {
		t.Fatalf("history after failed transaction %v", got)
	}

	proj.setFail(nil)
	tr.restart()
	start(t, tr, proj, nil, nil, testConfig())
	waitCommitted(t, tr, map[int32]int64{0: 3})
	if got := proj.history(); !reflect.DeepEqual(got, []string{"v1:1", "v1:2", "v1:3"}) {
		t.Fatalf("history %v", got)
	}
}

// TestTransientFailureRetriedInPlace: a failing record holds back the records behind
// it on its partition until it succeeds; other partitions keep going.
func TestTransientFailureRetriedInPlace(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	for _, e := range stream("v1", 1, 3) {
		tr.appendEvent(0, e)
	}
	for _, e := range stream("v2", 11, 2) {
		tr.appendEvent(1, e)
	}
	failures := 0
	proj.setFail(func(ev eventsourcing.Event) error {
		if ev.AggregateID == "v1" && ev.Version == 2 && failures < 3 {
			failures++
			return errors.New("deadlock detected")
		}
		return nil
	})
	start(t, tr, proj, nil, nil, testConfig())

	waitCommitted(t, tr, map[int32]int64{0: 3, 1: 2})
	var v1 []string
	for _, h := range proj.history() {
		if h[:3] == "v1:" {
			v1 = append(v1, h)
		}
	}
	if !reflect.DeepEqual(v1, []string{"v1:1", "v1:2", "v1:3"}) {
		t.Fatalf("v1 applied as %v", v1)
	}
	for _, r := range tr.retried() {
		if r.Partition != 0 || r.Offset != 1 {
			t.Fatalf("retried %s[%d]@%d, want only partition 0 offset 1", r.Topic, r.Partition, r.Offset)
		}
	}
	if len(tr.retried()) != 3 {
		t.Fatalf("%d retries, want 3", len(tr.retried()))
	}
}

// TestRestartResumesFromCommittedOffset: a restarted consumer continues after the
// last committed record and applies what was written while it was down.
func TestRestartResumesFromCommittedOffset(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	s := stream("v1", 1, 5)
	for _, e := range s[:3] {
		tr.appendEvent(0, e)
	}
	first := start(t, tr, proj, nil, nil, testConfig())
	waitCommitted(t, tr, map[int32]int64{0: 3})
	first.stop(t)

	for _, e := range s[3:] {
		tr.appendEvent(0, e)
	}
	tr.restart()
	second := start(t, tr, proj, nil, nil, testConfig())
	waitCommitted(t, tr, map[int32]int64{0: 5})
	if st := second.consumer.Status(); st.EventsApplied != 2 || st.DuplicatesSkipped != 0 {
		t.Fatalf("restarted consumer status %+v", st)
	}
	if got := proj.history(); len(got) != 5 || got[4] != "v1:5" {
		t.Fatalf("history %v", got)
	}
}
