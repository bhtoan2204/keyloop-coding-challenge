package probes

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"keyloop-coding-challenge/internal/application/messaging"
)

var (
	appliedDesc = prometheus.NewDesc("inventory_projection_events_applied_total",
		"Events applied to the read model by this consumer process.", nil, nil)
	duplicatesDesc = prometheus.NewDesc("inventory_projection_duplicates_skipped_total",
		"Re-delivered events skipped because their version was already applied.", nil, nil)
	repairedDesc = prometheus.NewDesc("inventory_projection_gap_events_repaired_total",
		"Events read from the event store to fill a version gap in the change feed.", nil, nil)
	failedDesc = prometheus.NewDesc("inventory_projection_record_failures_total",
		"Failed record attempts (a halted record counts once per retry).", nil, nil)
	readyDesc = prometheus.NewDesc("inventory_projection_ready",
		"1 when the consumer is ready (bootstrapped, broker reachable, no halted partition).", nil, nil)
	phaseDesc = prometheus.NewDesc("inventory_projection_phase",
		"1 for the consumer's current phase.", []string{"phase"}, nil)
	lagDesc = prometheus.NewDesc("inventory_projection_last_change_lag_seconds",
		"Apply time minus MySQL commit time of the last live change applied (clocks of different hosts).", nil, nil)
	haltedDesc = prometheus.NewDesc("inventory_projection_partition_halted",
		"1 while the partition is stopped at a failing record.", []string{"topic", "partition"}, nil)
	offsetDesc = prometheus.NewDesc("inventory_projection_partition_last_offset",
		"Offset of the last record processed on the partition.", []string{"topic", "partition"}, nil)
)

// consumerCollector reads the consumer's status at scrape time, so the consumer
// itself carries no metrics dependency. The counters reset when the process restarts.
type consumerCollector struct {
	status ConsumerStatus
}

// NewConsumerCollector exposes the projection consumer's progress and health.
func NewConsumerCollector(status ConsumerStatus) prometheus.Collector {
	return &consumerCollector{status: status}
}

func (c *consumerCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{appliedDesc, duplicatesDesc, repairedDesc, failedDesc, readyDesc, phaseDesc, lagDesc, haltedDesc, offsetDesc} {
		ch <- d
	}
}

func (c *consumerCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.status()
	ch <- prometheus.MustNewConstMetric(appliedDesc, prometheus.CounterValue, float64(s.EventsApplied))
	ch <- prometheus.MustNewConstMetric(duplicatesDesc, prometheus.CounterValue, float64(s.DuplicatesSkipped))
	ch <- prometheus.MustNewConstMetric(repairedDesc, prometheus.CounterValue, float64(s.GapEventsRepaired))
	ch <- prometheus.MustNewConstMetric(failedDesc, prometheus.CounterValue, float64(s.RecordsFailed))
	ch <- prometheus.MustNewConstMetric(readyDesc, prometheus.GaugeValue, boolValue(s.Ready))
	for _, phase := range []string{
		messaging.PhaseStarting,
		messaging.PhaseSyncing,
		messaging.PhaseConsuming,
		messaging.PhaseStopping,
		messaging.PhaseStopped,
	} {
		ch <- prometheus.MustNewConstMetric(phaseDesc, prometheus.GaugeValue, boolValue(s.Phase == phase), phase)
	}
	if s.LastChangeLagMs != nil {
		ch <- prometheus.MustNewConstMetric(lagDesc, prometheus.GaugeValue, float64(*s.LastChangeLagMs)/1000)
	}
	for _, p := range s.Partitions {
		partition := strconv.Itoa(int(p.Partition))
		ch <- prometheus.MustNewConstMetric(haltedDesc, prometheus.GaugeValue, boolValue(p.Failing), p.Topic, partition)
		ch <- prometheus.MustNewConstMetric(offsetDesc, prometheus.GaugeValue, float64(p.LastOffset), p.Topic, partition)
	}
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
