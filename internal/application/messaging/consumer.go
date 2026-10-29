package messaging

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Consumer applies the change feed to the read model.
type Consumer struct {
	transport Transport
	projector Projector
	history   EventHistory
	sync      Synchronizer
	decoder   *Decoder
	cfg       Config
	log       *slog.Logger
	health    *health
	now       func() time.Time
}

// NewConsumer wires the consumer. sync may be nil when cfg.StartupSync is false.
func NewConsumer(
	transport Transport,
	projector Projector,
	history EventHistory,
	sync Synchronizer,
	decoder *Decoder,
	cfg Config,
	log *slog.Logger,
) *Consumer {
	return &Consumer{
		transport: transport,
		projector: projector,
		history:   history,
		sync:      sync,
		decoder:   decoder,
		cfg:       cfg,
		log:       log,
		health:    newHealth(),
		now:       time.Now,
	}
}

// Run bootstraps, then consumes until ctx is cancelled. Each poll's batch is
// processed partition by partition in parallel (sequentially within a partition),
// then offsets are committed and failed partitions rewound, and only then may a
// rebalance move partitions. On shutdown the batch in progress stops between records;
// unprocessed records are not committed and are re-delivered to the next consumer.
func (c *Consumer) Run(ctx context.Context) error {
	defer c.transport.Close()
	defer c.health.setPhase(PhaseStopped)
	// Not ready as soon as shutdown begins, while the batch in progress drains.
	stopping := context.AfterFunc(ctx, func() { c.health.setPhase(PhaseStopping) })
	defer stopping()
	if err := c.bootstrap(ctx); err != nil {
		return err
	}
	c.health.setPhase(PhaseConsuming)
	for ctx.Err() == nil {
		if err := c.pollOnce(ctx); err != nil {
			return err
		}
	}
	return nil
}

// pollOnce runs one poll → process → commit → rewind → release cycle. It returns an
// error only when the consumer cannot continue.
func (c *Consumer) pollOnce(ctx context.Context) error {
	pollCtx, cancel := ctx, context.CancelFunc(func() {})
	if c.cfg.PollTimeout > 0 {
		pollCtx, cancel = context.WithTimeout(ctx, c.cfg.PollTimeout)
	}
	batches, err := c.transport.Poll(pollCtx)
	cancel()
	if errors.Is(err, ErrTransportClosed) {
		return err
	}
	if ctx.Err() == nil {
		c.health.transportResult(err)
		if err != nil {
			c.log.WarnContext(ctx, "change feed poll error", "error", err.Error())
		}
	}
	if len(batches) > 0 {
		c.processBatches(ctx, batches)
	}
	c.transport.Release()

	if errors.Is(err, ErrFeedDataLoss) && ctx.Err() == nil {
		// Records were skipped by the transport; everything they carried is committed
		// in the event store, so a sync applies it (a poison event among them keeps the
		// sync failing, and the consumer not ready, until it is fixed).
		c.log.ErrorContext(ctx, "change feed lost records; re-syncing from the event store", "error", err.Error())
		if c.sync == nil {
			return err
		}
		if err := c.syncFromEventStore(ctx, "feed data loss"); err != nil {
			return err
		}
		c.health.setPhase(PhaseConsuming)
		c.health.transportResult(nil)
	}
	if err != nil && len(batches) == 0 {
		wait(ctx, c.cfg.RetryMinDelay) // no hot loop while the broker keeps failing
	}
	return nil
}

func (c *Consumer) processBatches(ctx context.Context, batches []PartitionBatch) {
	results := make([]partitionResult, len(batches))
	var wg sync.WaitGroup
	for i := range batches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = c.processPartition(ctx, batches[i])
		}()
	}
	wg.Wait()
	c.commitProcessed(ctx, results)
	c.rewindFailed(ctx, results)
}

// Status returns a snapshot of the consumer's progress and health.
func (c *Consumer) Status() Status {
	return c.health.snapshot()
}

// Ready returns nil while the consumer is making progress (readiness probe).
func (c *Consumer) Ready(context.Context) error {
	return readiness(c.health.snapshot())
}

// AssignPartitions is called by the transport when partitions are assigned.
func (c *Consumer) AssignPartitions(topic string, partitions []int32) {
	c.health.assign(topic, partitions)
}

// ForgetPartitions is called by the transport when partitions are revoked or lost:
// another member resumes them from the committed offset and reports their health.
func (c *Consumer) ForgetPartitions(topic string, partitions []int32) {
	c.health.forget(topic, partitions)
}

func wait(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
