package probes_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"keyloop-coding-challenge/internal/application/messaging"
	"keyloop-coding-challenge/internal/presentation/probes"
)

func TestConsumerCollectorReportsStatus(t *testing.T) {
	lag := int64(1500)
	status := messaging.Status{
		Phase:             messaging.PhaseConsuming,
		Ready:             false,
		EventsApplied:     7,
		DuplicatesSkipped: 3,
		GapEventsRepaired: 2,
		RecordsFailed:     4,
		LastChangeLagMs:   &lag,
		Partitions: []messaging.PartitionStatus{
			{
				Topic:      "t",
				Partition:  1,
				LastOffset: 41,
				Failing:    true,
			},
		},
	}
	collector := probes.NewConsumerCollector(func() messaging.Status { return status })
	want := `
# HELP inventory_projection_events_applied_total Events applied to the read model by this consumer process.
# TYPE inventory_projection_events_applied_total counter
inventory_projection_events_applied_total 7
# HELP inventory_projection_duplicates_skipped_total Re-delivered events skipped because their version was already applied.
# TYPE inventory_projection_duplicates_skipped_total counter
inventory_projection_duplicates_skipped_total 3
# HELP inventory_projection_gap_events_repaired_total Events read from the event store to fill a version gap in the change feed.
# TYPE inventory_projection_gap_events_repaired_total counter
inventory_projection_gap_events_repaired_total 2
# HELP inventory_projection_record_failures_total Failed record attempts (a halted record counts once per retry).
# TYPE inventory_projection_record_failures_total counter
inventory_projection_record_failures_total 4
# HELP inventory_projection_ready 1 when the consumer is ready (bootstrapped, broker reachable, no halted partition).
# TYPE inventory_projection_ready gauge
inventory_projection_ready 0
# HELP inventory_projection_last_change_lag_seconds Apply time minus MySQL commit time of the last live change applied (clocks of different hosts).
# TYPE inventory_projection_last_change_lag_seconds gauge
inventory_projection_last_change_lag_seconds 1.5
# HELP inventory_projection_partition_halted 1 while the partition is stopped at a failing record.
# TYPE inventory_projection_partition_halted gauge
inventory_projection_partition_halted{partition="1",topic="t"} 1
# HELP inventory_projection_partition_last_offset Offset of the last record processed on the partition.
# TYPE inventory_projection_partition_last_offset gauge
inventory_projection_partition_last_offset{partition="1",topic="t"} 41
# HELP inventory_projection_phase 1 for the consumer's current phase.
# TYPE inventory_projection_phase gauge
inventory_projection_phase{phase="consuming"} 1
inventory_projection_phase{phase="starting"} 0
inventory_projection_phase{phase="stopped"} 0
inventory_projection_phase{phase="stopping"} 0
inventory_projection_phase{phase="syncing"} 0
`
	err := testutil.CollectAndCompare(collector, strings.NewReader(want),
		"inventory_projection_events_applied_total",
		"inventory_projection_duplicates_skipped_total",
		"inventory_projection_gap_events_repaired_total",
		"inventory_projection_partition_last_offset",
		"inventory_projection_phase",
		"inventory_projection_record_failures_total",
		"inventory_projection_ready",
		"inventory_projection_last_change_lag_seconds",
		"inventory_projection_partition_halted",
	)
	if err != nil {
		t.Fatal(err)
	}
}
