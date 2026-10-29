//go:build integration && pipeline

package pipeline_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/application/messaging"
	"keyloop-coding-challenge/internal/infrastructure/kafka"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/package/eventsourcing"
)

type consumerOpts struct {
	group       string // default: the env's group
	startupSync bool
	// wrap decorates the Kafka transport (fault injection).
	wrap func(messaging.Transport) messaging.Transport
	// extra projectors run after VehicleProjector in the same transaction.
	extra []projectors.Projector
}

// runningConsumer is the production consumer (kafka.Transport, ConsumerAdapter,
// MySQL history) running in the background.
type runningConsumer struct {
	consumer *messaging.Consumer
	cancel   context.CancelFunc
	done     chan error
	once     sync.Once
}

func (e *env) startConsumer(t *testing.T, o consumerOpts) *runningConsumer {
	t.Helper()
	group := o.group
	if group == "" {
		group = e.group
	}
	store := viewstore.NewPostgres(e.pg)
	projs := append([]projectors.Projector{&projectors.VehicleProjector{}}, o.extra...)
	adapter := projection.NewConsumerAdapter(
		projection.NewProjection(store, projs...),
		projection.NewRunner(e.events, store, projs...),
		e.events,
	)
	listener := &lateListener{}
	kt, err := kafka.NewTransport(kafka.Config{
		Brokers:        e.brokers,
		Topic:          e.topic,
		Group:          group,
		MaxPollRecords: 50,
		SessionTimeout: 10 * time.Second,
	}, testLog(), listener)
	if err != nil {
		t.Fatal(err)
	}
	var transport messaging.Transport = kt
	if o.wrap != nil {
		transport = o.wrap(kt)
	}
	cfg := messaging.DefaultConfig()
	cfg.StartupSync = o.startupSync
	cfg.RetryMinDelay = 50 * time.Millisecond
	cfg.RetryMaxDelay = time.Second
	c := messaging.NewConsumer(
		transport,
		adapter,
		adapter,
		adapter,
		messaging.NewDecoder(e.serializer),
		cfg,
		testLog(),
	)
	listener.consumer = c

	runCtx, cancel := context.WithCancel(context.Background())
	r := &runningConsumer{
		consumer: c,
		cancel:   cancel,
		done:     make(chan error, 1),
	}
	go func() { r.done <- c.Run(runCtx) }()
	t.Cleanup(func() { r.stop(t) })
	return r
}

// stop shuts the consumer down gracefully (as on SIGTERM); later calls do nothing.
func (r *runningConsumer) stop(t *testing.T) {
	t.Helper()
	r.once.Do(func() {
		r.cancel()
		select {
		case err := <-r.done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("consumer: %v", err)
			}
		case <-time.After(60 * time.Second):
			t.Error("consumer did not stop")
		}
	})
}

// commitFails simulates a crash between the read-model commit and the offset
// commit: every offset commit is lost.
type commitFails struct {
	messaging.Transport
}

func (commitFails) Commit(context.Context, []messaging.Record) error {
	return errors.New("simulated crash before the offset commit")
}

// failingProjector fails, inside the read-model transaction and after
// VehicleProjector has written, for one aggregate version while failing is set. The
// transaction must roll back everything VehicleProjector wrote.
type failingProjector struct {
	aggregateID string
	version     int
	failing     atomic.Bool
	failures    atomic.Int32
}

func (f *failingProjector) Handle(_ context.Context, _ viewstore.ViewStore, ev eventsourcing.Event) error {
	if f.failing.Load() && ev.AggregateID == f.aggregateID && ev.Version == f.version {
		f.failures.Add(1)
		return errors.New("simulated failure inside the projection transaction")
	}
	return nil
}

// lateListener forwards partition assignments to a consumer created after the
// transport (the transport joins the group only on its first poll).
type lateListener struct {
	consumer *messaging.Consumer
}

func (l *lateListener) AssignPartitions(topic string, partitions []int32) {
	l.consumer.AssignPartitions(topic, partitions)
}

func (l *lateListener) ForgetPartitions(topic string, partitions []int32) {
	l.consumer.ForgetPartitions(topic, partitions)
}
