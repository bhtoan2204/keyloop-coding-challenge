package kafka

import (
	"context"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"keyloop-coding-challenge/internal/application/messaging"
)

// Commit synchronously commits Offset+1 for each record's partition. Called with
// rebalances blocked, so the partitions are still owned by this member.
func (t *Transport) Commit(ctx context.Context, processed []messaging.Record) error {
	client := t.currentClient()
	if client == nil {
		return messaging.ErrTransportClosed
	}
	records := make([]*kgo.Record, 0, len(processed))
	for _, p := range processed {
		records = append(records, &kgo.Record{
			Topic:       p.Topic,
			Partition:   p.Partition,
			LeaderEpoch: p.LeaderEpoch,
			Offset:      p.Offset,
		})
	}
	return client.CommitRecords(ctx, records...)
}

// Retry rewinds the record's partition to the record and pauses fetching it for
// delay. Rewinding drops anything already buffered for the partition, so no later
// record of it can be delivered before the failed one. Called with rebalances
// blocked, as SetOffsets requires; if the partition moves to another member
// meanwhile, that member starts from the committed offset, which is not past rec.
// A newer Retry of the same partition replaces the pending resume.
func (t *Transport) Retry(rec messaging.Record, delay time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.client == nil {
		return
	}
	client := t.client
	partition := map[string][]int32{rec.Topic: {rec.Partition}}
	client.PauseFetchPartitions(partition)
	client.SetOffsets(map[string]map[int32]kgo.EpochOffset{
		rec.Topic: {
			rec.Partition: {
				Epoch:  -1,
				Offset: rec.Offset,
			},
		},
	})

	key := partitionKey(rec.Topic, rec.Partition)
	if pending := t.retries[key]; pending != nil {
		pending.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		t.mu.Lock()
		current := !t.closed && t.retries[key] == timer
		if current {
			delete(t.retries, key)
		}
		t.mu.Unlock()
		if current {
			client.ResumeFetchPartitions(partition)
		}
	})
	t.retries[key] = timer
}
