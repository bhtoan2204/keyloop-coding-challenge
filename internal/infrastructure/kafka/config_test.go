package kafka

import (
	"testing"
	"time"
)

func TestConfigRequiresBrokersTopicAndGroup(t *testing.T) {
	valid := Config{
		Brokers: []string{"127.0.0.1:9092"},
		Topic:   "inventory.inventory.es_event",
		Group:   "inventory-projector",
	}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"no brokers": func(c *Config) { c.Brokers = nil },
		"no topic":   func(c *Config) { c.Topic = "" },
		"no group":   func(c *Config) { c.Group = "" },
	} {
		cfg := valid
		mutate(&cfg)
		if err := cfg.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	d := valid.withDefaults()
	if d.MaxPollRecords != 100 || d.SessionTimeout != 45*time.Second || d.RebalanceTimeout != time.Minute || d.ClientID == "" {
		t.Fatalf("defaults: %+v", d)
	}
}
