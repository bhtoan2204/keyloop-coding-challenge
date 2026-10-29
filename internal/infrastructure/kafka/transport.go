package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"keyloop-coding-challenge/internal/application/messaging"
)

var _ messaging.Transport = (*Transport)(nil)

// PartitionListener is told which partitions this member owns (the consumer's health
// reporting). Called from the client's group goroutine.
type PartitionListener interface {
	AssignPartitions(topic string, partitions []int32)
	ForgetPartitions(topic string, partitions []int32)
}

// Transport consumes the change-feed topic in a consumer group.
//
//   - Auto-commit is off: offsets are committed only through Commit, which the
//     consumer calls after the read-model transactions have committed.
//   - Rebalances are blocked from a poll until Release, so a partition cannot move to
//     another member while its batch is being applied and committed.
//   - A new group starts at the beginning of the topic (the connector's snapshot).
//   - The client, and with it the group membership, is created by the first Poll:
//     the member does not own partitions while the consumer is still bootstrapping.
type Transport struct {
	cfg      Config
	log      *slog.Logger
	listener PartitionListener
	dial     dialHealth

	mu      sync.Mutex
	client  *kgo.Client
	retries map[string]*time.Timer // "topic/partition" → pending resume
	closed  bool
}

// NewTransport validates the configuration; it does not connect yet. listener may
// be nil.
func NewTransport(cfg Config, log *slog.Logger, listener PartitionListener) (*Transport, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Transport{
		cfg:      cfg.withDefaults(),
		log:      log,
		listener: listener,
		retries:  make(map[string]*time.Timer),
	}, nil
}

func (t *Transport) connect() (*kgo.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, messaging.ErrTransportClosed
	}
	if t.client != nil {
		return t.client, nil
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(t.cfg.Brokers...),
		kgo.ClientID(t.cfg.ClientID),
		kgo.ConsumerGroup(t.cfg.Group),
		kgo.ConsumeTopics(t.cfg.Topic),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.SessionTimeout(t.cfg.SessionTimeout),
		kgo.RebalanceTimeout(t.cfg.RebalanceTimeout),
		kgo.WithHooks(&t.dial),
		kgo.OnPartitionsAssigned(t.onPartitions("partitions assigned", PartitionListener.AssignPartitions)),
		kgo.OnPartitionsRevoked(t.onPartitions("partitions revoked", PartitionListener.ForgetPartitions)),
		kgo.OnPartitionsLost(t.onPartitions("partitions lost", PartitionListener.ForgetPartitions)),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka client: %w", err)
	}
	t.client = client
	return client, nil
}

// Poll returns the next records by partition. Errors of individual partitions are
// joined; records of the other partitions are still returned. A partition reset past
// unconsumed records is reported as messaging.ErrFeedDataLoss, and a broker that
// cannot be reached as a dial error.
func (t *Transport) Poll(ctx context.Context) ([]messaging.PartitionBatch, error) {
	client, err := t.connect()
	if err != nil {
		return nil, err
	}
	fetches := client.PollRecords(ctx, t.cfg.MaxPollRecords)
	if fetches.IsClientClosed() {
		return nil, messaging.ErrTransportClosed
	}
	var errs []error
	fetches.EachError(func(topic string, partition int32, err error) {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		var loss *kgo.ErrDataLoss
		if errors.As(err, &loss) {
			err = fmt.Errorf("%w: %w", messaging.ErrFeedDataLoss, err)
		}
		errs = append(errs, fmt.Errorf("%s[%d]: %w", topic, partition, err))
	})
	if err := t.dial.err(); err != nil {
		errs = append(errs, err)
	}
	return toBatches(fetches), errors.Join(errs...)
}

// toBatches groups the fetched records by partition (a partition can appear in more
// than one fetch of a poll), keeping offset order.
func toBatches(fetches kgo.Fetches) []messaging.PartitionBatch {
	index := make(map[string]int)
	var batches []messaging.PartitionBatch
	fetches.EachPartition(func(p kgo.FetchTopicPartition) {
		if len(p.Records) == 0 {
			return
		}
		key := partitionKey(p.Topic, p.Partition)
		i, ok := index[key]
		if !ok {
			i = len(batches)
			index[key] = i
			batches = append(batches, messaging.PartitionBatch{
				Topic:     p.Topic,
				Partition: p.Partition,
			})
		}
		for _, r := range p.Records {
			batches[i].Records = append(batches[i].Records, messaging.Record{
				Topic:       r.Topic,
				Partition:   r.Partition,
				Offset:      r.Offset,
				LeaderEpoch: r.LeaderEpoch,
				Key:         r.Key,
				Value:       r.Value,
			})
		}
	})
	return batches
}

// Release lets a rebalance that waited for the current batch proceed.
func (t *Transport) Release() {
	if client := t.currentClient(); client != nil {
		client.AllowRebalance()
	}
}

// Close stops pending resumes, then leaves the group. Nothing is committed here:
// uncommitted records are re-delivered to the next owner of their partition.
func (t *Transport) Close() {
	t.mu.Lock()
	t.closed = true
	for _, timer := range t.retries {
		timer.Stop()
	}
	t.retries = nil
	client := t.client
	t.mu.Unlock()
	if client != nil {
		client.CloseAllowingRebalance()
	}
}

func (t *Transport) currentClient() *kgo.Client {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.client
}

func (t *Transport) onPartitions(msg string, notify func(PartitionListener, string, []int32)) func(context.Context, *kgo.Client, map[string][]int32) {
	return func(ctx context.Context, _ *kgo.Client, partitions map[string][]int32) {
		for topic, ps := range partitions {
			t.log.InfoContext(ctx, msg,
				"topic", topic,
				"partitions", ps)
			if t.listener != nil {
				notify(t.listener, topic, ps)
			}
		}
	}
}

func partitionKey(topic string, partition int32) string {
	return fmt.Sprintf("%s/%d", topic, partition)
}
