package messaging

import "time"

// Config tunes the consumer. The transport (brokers, topic, group) is configured in
// the infrastructure adapter.
type Config struct {
	// StartupSync applies, before the first poll, every event committed to the event
	// store so far (Synchronizer). It closes any hole the change feed may have
	// (retention, a lost connector offset) for everything committed before startup.
	StartupSync bool
	// ApplyTimeout bounds one record's read-model transaction.
	ApplyTimeout time.Duration
	// RetryMinDelay and RetryMaxDelay bound the backoff before a failed record (or the
	// startup sync) is retried. Poison records always wait RetryMaxDelay.
	RetryMinDelay time.Duration
	RetryMaxDelay time.Duration
	// PollTimeout bounds one wait for records. An idle feed then still reports
	// transport health regularly (a past error clears once a poll succeeds).
	PollTimeout time.Duration
}

// DefaultConfig is the production configuration. A batch may take up to
// (records per poll) × ApplyTimeout in the worst case; the transport's rebalance
// timeout should cover a normal batch (see kafka.Config).
func DefaultConfig() Config {
	return Config{
		StartupSync:   true,
		ApplyTimeout:  30 * time.Second,
		RetryMinDelay: 500 * time.Millisecond,
		RetryMaxDelay: 30 * time.Second,
		PollTimeout:   5 * time.Second,
	}
}

// retryDelay is the wait before attempt failures+1: exponential for transient errors,
// the maximum for poison (an operator has to fix something first).
func (c Config) retryDelay(failures int, poison bool) time.Duration {
	if poison || failures > 16 {
		return c.RetryMaxDelay
	}
	delay := c.RetryMinDelay << max(failures-1, 0)
	return min(delay, c.RetryMaxDelay)
}
