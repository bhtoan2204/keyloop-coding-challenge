package messaging

// Record is one message of the change feed as delivered by the transport.
type Record struct {
	Topic       string
	Partition   int32
	Offset      int64
	LeaderEpoch int32
	Key         []byte
	Value       []byte
}

// PartitionBatch holds the polled records of one partition, in offset order.
type PartitionBatch struct {
	Topic     string
	Partition int32
	Records   []Record
}

// partitionID identifies a partition in the consumer's bookkeeping.
type partitionID struct {
	topic     string
	partition int32
}

func (r Record) partitionID() partitionID {
	return partitionID{
		topic:     r.Topic,
		partition: r.Partition,
	}
}

func (b PartitionBatch) partitionID() partitionID {
	return partitionID{
		topic:     b.Topic,
		partition: b.Partition,
	}
}
