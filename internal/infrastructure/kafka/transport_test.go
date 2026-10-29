package kafka

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"keyloop-coding-challenge/internal/application/messaging"
)

// TestTransportBeforeConnectAndAfterClose: the client (and the group membership) is
// created by the first Poll, so nothing touches Kafka before it; after Close every
// call is a no-op or reports the transport closed.
func TestTransportBeforeConnectAndAfterClose(t *testing.T) {
	tr, err := NewTransport(Config{
		Brokers: []string{"127.0.0.1:1"},
		Topic:   "t",
		Group:   "g",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if tr.currentClient() != nil {
		t.Fatal("client created before the first poll")
	}
	rec := messaging.Record{
		Topic:     "t",
		Partition: 0,
		Offset:    3,
	}
	tr.Retry(rec, time.Millisecond) // nothing polled yet: nothing to rewind
	tr.Release()
	if err := tr.Commit(context.Background(), []messaging.Record{rec}); !errors.Is(err, messaging.ErrTransportClosed) {
		t.Fatalf("commit without a client: %v", err)
	}

	tr.Close()
	if _, err := tr.Poll(context.Background()); !errors.Is(err, messaging.ErrTransportClosed) {
		t.Fatalf("poll after close: %v", err)
	}
	if tr.currentClient() != nil {
		t.Fatal("poll after close created a client")
	}
	tr.Retry(rec, time.Millisecond)
	tr.Close() // idempotent
}

// TestToBatchesMergesAPartitionAcrossFetches: a partition appearing in two fetches of
// one poll becomes ONE batch in offset order, so it is never processed by two
// goroutines at once.
func TestToBatchesMergesAPartitionAcrossFetches(t *testing.T) {
	fetch := func(partition int32, offsets ...int64) kgo.Fetch {
		var records []*kgo.Record
		for _, o := range offsets {
			records = append(records, &kgo.Record{
				Topic:     "t",
				Partition: partition,
				Offset:    o,
			})
		}
		return kgo.Fetch{Topics: []kgo.FetchTopic{{
			Topic: "t",
			Partitions: []kgo.FetchPartition{{
				Partition: partition,
				Records:   records,
			}},
		}}}
	}
	batches := toBatches(kgo.Fetches{fetch(0, 1, 2), fetch(1, 7), fetch(0, 3)})
	if len(batches) != 2 {
		t.Fatalf("%d batches, want 2: %+v", len(batches), batches)
	}
	var offsets []int64
	for _, r := range batches[0].Records {
		offsets = append(offsets, r.Offset)
	}
	if batches[0].Partition != 0 || !reflect.DeepEqual(offsets, []int64{1, 2, 3}) {
		t.Fatalf("partition 0 batch: %+v", batches[0])
	}
}
