package messaging

import (
	"context"
	"fmt"
)

// partitionResult is what happened to one partition's batch.
type partitionResult struct {
	// processed is the last record applied or skipped as a duplicate. Its read-model
	// transaction has committed, so its offset may be committed.
	processed *Record
	// failed is the first record that could not be processed; records after it in the
	// batch were not attempted.
	failed *Record
	err    error
}

// processPartition handles one partition's records strictly in offset order: all
// events of an aggregate share a partition, so this is what keeps them in version
// order. It stops at the first failure and, on shutdown, between records.
func (c *Consumer) processPartition(ctx context.Context, batch PartitionBatch) partitionResult {
	var res partitionResult
	for i := range batch.Records {
		if ctx.Err() != nil {
			return res // shutting down: the rest is not committed and is re-delivered
		}
		rec := batch.Records[i]
		if err := c.processRecord(ctx, rec); err != nil {
			res.failed = &rec
			res.err = err
			return res
		}
		res.processed = &rec
	}
	return res
}

// processRecord decodes and applies one record. The read-model transaction gets its
// own deadline and is not cancelled by shutdown: an apply in progress finishes (or
// rolls back) on its own instead of being interrupted half-way.
func (c *Consumer) processRecord(ctx context.Context, rec Record) error {
	changed, err := c.decoder.Decode(rec)
	if err != nil {
		return fmt.Errorf("%s[%d]@%d: %w", rec.Topic, rec.Partition, rec.Offset, err)
	}
	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.cfg.ApplyTimeout)
	defer cancel()
	applied, repaired, err := c.apply(applyCtx, changed.Event)
	if err != nil {
		c.health.recordRepaired(repaired) // committed even though the record itself failed
		return fmt.Errorf("%s[%d]@%d %s v%d: %w", rec.Topic, rec.Partition, rec.Offset,
			changed.Event.AggregateID, changed.Event.Version, err)
	}
	c.health.recordEvent(changed, applied, repaired, c.now())
	return nil
}
