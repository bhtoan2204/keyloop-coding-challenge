package messaging_test

import (
	"context"
	"strings"
	"testing"

	"keyloop-coding-challenge/internal/application/messaging"
	"keyloop-coding-challenge/package/eventsourcing"
)

// TestPoisonRecordIsNeverSkipped: a record of a newer payload schema stops its
// partition; later records are not applied and the offset stays before it.
func TestPoisonRecordIsNeverSkipped(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	s := stream("v1", 1, 3)
	tr.appendEvent(0, s[0])
	tr.append(0, s[1].key(), s[1].changeValue("c", 2))
	tr.appendEvent(0, s[2])
	r := start(t, tr, proj, nil, nil, testConfig())

	eventually(t, "several retries", func() bool { return len(tr.retried()) >= 3 })
	if got := tr.committedOffset(0); got != 1 {
		t.Fatalf("committed offset %d, want 1", got)
	}
	if proj.version("v1") != 1 {
		t.Fatalf("applied up to v%d past a poison record", proj.version("v1"))
	}
	st := r.consumer.Status()
	if st.Ready || len(st.Partitions) != 1 || !st.Partitions[0].Poison || st.Partitions[0].FailingOffset != 1 ||
		!strings.Contains(st.Partitions[0].LastError, "unsupported event schema version") {
		t.Fatalf("status %+v", st)
	}
}

// TestStartupSyncCompletesBeforeConsuming: the bootstrap sync is retried until it
// succeeds, and no record is applied before it has.
func TestStartupSyncCompletesBeforeConsuming(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	tr.appendEvent(0, stream("v1", 1, 1)[0])
	sync := &fakeSync{failures: 2}
	proj.setFail(func(eventsourcing.Event) error {
		sync.mu.Lock()
		defer sync.mu.Unlock()
		if sync.calls < 3 {
			t.Errorf("event applied after %d sync attempts, before the sync succeeded", sync.calls)
		}
		return nil
	})
	cfg := testConfig()
	cfg.StartupSync = true
	r := start(t, tr, proj, nil, sync, cfg)

	waitCommitted(t, tr, map[int32]int64{0: 1})
	if sync.calls != 3 {
		t.Fatalf("%d sync attempts, want 3", sync.calls)
	}
	if err := r.consumer.Ready(context.Background()); err != nil {
		t.Fatalf("not ready after bootstrap: %v", err)
	}
}

func TestReadinessFollowsPhaseAndRevokedPartitions(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	s := stream("v1", 1, 2)
	tr.appendEvent(0, s[0])
	tr.append(0, s[1].key(), []byte("{")) // poison
	c := messaging.NewConsumer(
		tr,
		proj,
		&fakeHistory{},
		nil,
		messaging.NewDecoder(newSerializer(t)),
		testConfig(),
		discard,
	)
	if err := c.Ready(context.Background()); err == nil || !strings.Contains(err.Error(), "starting") {
		t.Fatalf("ready before Run: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	eventually(t, "halted", func() bool { return len(tr.retried()) > 0 })
	if c.Ready(context.Background()) == nil {
		t.Fatal("ready while a partition is halted")
	}
	// The halted partition moves to another member: this one no longer reports it.
	// As in Kafka, the revoke happens between batches: stop delivery and wait until
	// no batch is in flight first.
	tr.block()
	eventually(t, "no batch in flight", tr.idle)
	c.ForgetPartitions(topic, []int32{0})
	if err := c.Ready(context.Background()); err != nil {
		t.Fatalf("not ready after the halted partition was revoked: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st := c.Status(); st.Phase != messaging.PhaseStopped || st.Ready {
		t.Fatalf("after stop: %+v", st)
	}
}
