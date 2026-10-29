package kafka

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

var _ kgo.HookBrokerConnect = (*dialHealth)(nil)

// dialHealth remembers whether the last connection attempt to the brokers failed.
// The client retries unreachable brokers internally and keeps a poll waiting rather
// than returning an error, so without this the consumer would look healthy while it
// cannot reach Kafka at all.
type dialHealth struct {
	mu      sync.Mutex
	lastErr error
	since   time.Time
}

func (d *dialHealth) OnBrokerConnect(meta kgo.BrokerMetadata, _ time.Duration, _ net.Conn, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err == nil {
		d.lastErr = nil
		return
	}
	if d.lastErr == nil {
		d.since = time.Now()
	}
	d.lastErr = fmt.Errorf("cannot connect to broker %s:%d: %w", meta.Host, meta.Port, err)
}

func (d *dialHealth) err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastErr == nil {
		return nil
	}
	return fmt.Errorf("%w (failing since %s)", d.lastErr, d.since.UTC().Format(time.RFC3339))
}
