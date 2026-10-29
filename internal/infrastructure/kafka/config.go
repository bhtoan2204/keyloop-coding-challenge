// Package kafka is the Kafka transport of the projection consumer
// (messaging.Transport), built on franz-go with a consumer group.
package kafka

import (
	"errors"
	"time"
)

// Config selects the change-feed topic and the consumer group.
type Config struct {
	Brokers  []string
	Topic    string
	Group    string
	ClientID string
	// MaxPollRecords bounds one batch: rebalances wait while a batch is processed.
	// A read-model transaction takes milliseconds, so 100 records normally finish far
	// within RebalanceTimeout; on a database stalled long enough to exceed it the
	// member is evicted, its commit fails and the records are re-delivered and skipped
	// by version (liveness, not correctness).
	MaxPollRecords int
	// SessionTimeout and RebalanceTimeout are the group membership timeouts.
	SessionTimeout   time.Duration
	RebalanceTimeout time.Duration
}

func (c Config) validate() error {
	switch {
	case len(c.Brokers) == 0:
		return errors.New("kafka: no brokers")
	case c.Topic == "":
		return errors.New("kafka: no topic")
	case c.Group == "":
		return errors.New("kafka: no consumer group")
	}
	return nil
}

func (c Config) withDefaults() Config {
	if c.ClientID == "" {
		c.ClientID = "inventory-projector"
	}
	if c.MaxPollRecords <= 0 {
		c.MaxPollRecords = 100
	}
	if c.SessionTimeout <= 0 {
		c.SessionTimeout = 45 * time.Second
	}
	if c.RebalanceTimeout <= 0 {
		c.RebalanceTimeout = 60 * time.Second
	}
	return c
}
