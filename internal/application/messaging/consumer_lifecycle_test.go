package messaging_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"keyloop-coding-challenge/internal/application/messaging"
	"keyloop-coding-challenge/package/eventsourcing"
)

// TestBatchCycleCommitsThenRewindsThenReleases: within one batch the consumer commits
// what was applied, rewinds failed partitions, and only then lets a rebalance happen.
func TestBatchCycleCommitsThenRewindsThenReleases(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	tr.appendEvent(0, stream("ok", 1, 1)[0])
	bad := stream("bad", 11, 1)[0]
	tr.append(1, bad.key(), bad.changeValue("c", 2)) // poison
	start(t, tr, proj, nil, nil, testConfig())

	eventually(t, "first cycle", func() bool { return len(tr.operations()) >= 4 })
	if got := tr.operations()[:4]; !reflect.DeepEqual(got, []string{"poll", "commit", "retry", "release"}) {
		t.Fatalf("first batch cycle %v", got)
	}
}

// TestShutdownLetsTheApplyInProgressFinish: on SIGTERM the record being applied
// completes with an uncancelled context and is committed; records after it are not
// processed or committed, and the consumer reports not ready while it drains.
func TestShutdownLetsTheApplyInProgressFinish(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	for _, e := range stream("v1", 1, 3) {
		tr.appendEvent(0, e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var applyCtxErr error
	blocking := &ctxProjector{
		fakeProjector: proj,
		before: func(ctx context.Context, ev eventsourcing.Event) {
			if ev.Version != 2 {
				return
			}
			close(entered)
			<-release
			applyCtxErr = ctx.Err()
		},
	}
	c := messaging.NewConsumer(tr, blocking, &fakeHistory{}, nil, messaging.NewDecoder(newSerializer(t)), testConfig(), discard)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	<-entered
	cancel()
	eventually(t, "stopping phase", func() bool { return c.Status().Phase == messaging.PhaseStopping })
	if err := c.Ready(context.Background()); err == nil || !strings.Contains(err.Error(), "stopping") {
		t.Fatalf("ready while draining: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if applyCtxErr != nil {
		t.Fatalf("apply in progress saw a cancelled context: %v", applyCtxErr)
	}
	if got := tr.committedOffset(0); got != 2 {
		t.Fatalf("committed offset %d, want 2 (v1 and the drained v2)", got)
	}
	if got := proj.history(); !reflect.DeepEqual(got, []string{"v1:1", "v1:2"}) {
		t.Fatalf("history %v: v3 must wait for the next consumer", got)
	}
}

// ctxProjector runs before(ctx, ev) ahead of the fake projector's Apply.
type ctxProjector struct {
	*fakeProjector
	before func(context.Context, eventsourcing.Event)
}

func (p *ctxProjector) Apply(ctx context.Context, ev eventsourcing.Event) (bool, error) {
	p.before(ctx, ev)
	return p.fakeProjector.Apply(ctx, ev)
}

// TestFeedDataLossResyncsFromEventStore: when the transport skipped unconsumed
// records, the consumer syncs from the event store before continuing.
func TestFeedDataLossResyncsFromEventStore(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	sync := &fakeSync{}
	cfg := testConfig()
	cfg.StartupSync = true
	r := start(t, tr, proj, nil, sync, cfg)
	eventually(t, "startup sync", func() bool { return r.consumer.Status().Phase == messaging.PhaseConsuming })

	tr.failNextPoll(fmt.Errorf("t[0]: %w", messaging.ErrFeedDataLoss))
	eventually(t, "re-sync after data loss", func() bool {
		sync.mu.Lock()
		defer sync.mu.Unlock()
		return sync.calls == 2
	})
	eventually(t, "ready again", func() bool { return r.consumer.Ready(context.Background()) == nil })
}

func TestFeedDataLossWithoutSynchronizerStops(t *testing.T) {
	tr := newFakeTransport()
	tr.failNextPoll(messaging.ErrFeedDataLoss)
	c := messaging.NewConsumer(tr, newFakeProjector(), &fakeHistory{}, nil, messaging.NewDecoder(newSerializer(t)), testConfig(), discard)
	if err := c.Run(context.Background()); !errors.Is(err, messaging.ErrFeedDataLoss) {
		t.Fatalf("Run = %v, want ErrFeedDataLoss: records were skipped and nothing can restore them", err)
	}
}

// TestTransportErrorClearsAfterHealthyPoll: a broker error makes the consumer not
// ready only until a poll succeeds, even when no records arrive.
func TestTransportErrorClearsAfterHealthyPoll(t *testing.T) {
	tr := newFakeTransport()
	r := start(t, tr, newFakeProjector(), nil, nil, testConfig())
	eventually(t, "consuming", func() bool { return r.consumer.Ready(context.Background()) == nil })

	tr.failNextPoll(errors.New("cannot connect to broker"))
	eventually(t, "not ready", func() bool { return r.consumer.Ready(context.Background()) != nil })
	eventually(t, "ready after an idle healthy poll", func() bool { return r.consumer.Ready(context.Background()) == nil })
}

// TestLostPartitionIsNotResurrectedByItsBatch: a partition lost while its batch is
// still being applied (session expiry) is not reported again by that batch.
func TestLostPartitionIsNotResurrectedByItsBatch(t *testing.T) {
	tr, proj := newFakeTransport(), newFakeProjector()
	bad := stream("bad", 1, 1)[0]
	tr.append(0, bad.key(), bad.changeValue("c", 1))
	var c *messaging.Consumer
	var once sync.Once
	proj.setFail(func(eventsourcing.Event) error {
		once.Do(func() { c.ForgetPartitions(topic, []int32{0}) }) // lost mid-batch
		return errors.New("deadlock")
	})
	c = messaging.NewConsumer(tr, proj, &fakeHistory{}, nil, messaging.NewDecoder(newSerializer(t)), testConfig(), discard)
	c.AssignPartitions(topic, []int32{0})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()

	eventually(t, "the failing batch finished", func() bool { return len(tr.retried()) >= 1 && tr.idle() })
	if st := c.Status(); len(st.Partitions) != 0 || !st.Ready {
		t.Fatalf("lost partition reported after its batch: %+v", st)
	}
}
