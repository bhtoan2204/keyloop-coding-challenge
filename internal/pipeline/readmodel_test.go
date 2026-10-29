//go:build integration && pipeline

package pipeline_test

import (
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/internal/testsupport/pgtest"
)

// dumpReadModel renders every projected row; projection_aggregate without its
// wall-clock updated_at.
func dumpReadModel(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var rows []string
	// UNION ALL does not keep the subqueries' order: order the whole result.
	err := db.Raw(`
		SELECT line FROM (
			SELECT 'vehicle ' || row_to_json(v)::text AS line FROM inventory_vehicle v
			UNION ALL
			SELECT 'history ' || row_to_json(h)::text FROM vehicle_action_history h
			UNION ALL
			SELECT 'applied ' || json_build_object('aggregate_id', aggregate_id, 'version', version,
				'last_event_id', last_event_id)::text FROM projection_aggregate
		) rows ORDER BY line COLLATE "C"`).
		Scan(&rows).Error
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// expectedReadModel rebuilds a fresh read model from the MySQL history (the existing
// rebuild path) and dumps it: what the live pipeline must converge to.
func (e *env) expectedReadModel(t *testing.T) []string {
	t.Helper()
	db, _ := pgtest.NewDatabase(t)
	if err := viewstore.Migrate(db); err != nil {
		t.Fatal(err)
	}
	runner := projection.NewRunner(e.events, viewstore.NewPostgres(db), &projectors.VehicleProjector{})
	if _, err := runner.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	return dumpReadModel(t, db)
}

// waitProjected waits until every aggregate in MySQL is projected at its current
// version.
func (e *env) waitProjected(t *testing.T, timeout time.Duration) {
	t.Helper()
	waitFor(t, "read model to catch up with MySQL", timeout, func() bool {
		return reflect.DeepEqual(e.writeVersions(t), e.projectedVersions(t))
	})
}

func (e *env) writeVersions(t *testing.T) map[string]int {
	t.Helper()
	var rows []struct {
		ID      string
		Version int
	}
	if err := e.mysql.Raw(`SELECT id, version FROM es_aggregate`).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Version
	}
	return out
}

func (e *env) projectedVersions(t *testing.T) map[string]int {
	t.Helper()
	var rows []struct {
		AggregateID string
		Version     int
	}
	if err := e.pg.Raw(`SELECT aggregate_id, version FROM projection_aggregate`).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.AggregateID] = r.Version
	}
	return out
}

// assertMatchesRebuild compares the live read model with a rebuild from MySQL. Equal
// rows mean nothing was lost, nothing applied twice (history included) and no
// version applied out of order.
func (e *env) assertMatchesRebuild(t *testing.T) {
	t.Helper()
	got, want := dumpReadModel(t, e.pg), e.expectedReadModel(t)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("live read model differs from a rebuild:\n live:    %d rows %v\n rebuild: %d rows %v", len(got), got, len(want), want)
	}
	var events, history int64
	if err := e.mysql.Raw(`SELECT count(*) FROM es_event WHERE event_type <> 'VehicleStockedEvent'`).Scan(&events).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.pg.Raw(`SELECT count(*) FROM vehicle_action_history`).Scan(&history).Error; err != nil {
		t.Fatal(err)
	}
	// Every non-stock event of this workload records one history row (each sale
	// closes an active action), so equal counts also rule out duplicated history.
	if history != events {
		t.Fatalf("%d history rows for %d non-stock events", history, events)
	}
	t.Logf("read model matches rebuild: %d rows, %d history rows for %d non-stock events", len(got), history, events)
}

func (e *env) historyVersions(t *testing.T, vehicleID string) []int {
	t.Helper()
	var versions []int
	if err := e.pg.Raw(`SELECT version FROM vehicle_action_history WHERE vehicle_id = ? ORDER BY version`, vehicleID).
		Scan(&versions).Error; err != nil {
		t.Fatal(err)
	}
	return versions
}
