//go:build integration

package presentation_test

import (
	"net/http/httptest"
	"testing"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/commandstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/referencedata"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/internal/presentation"
	"keyloop-coding-challenge/internal/testsupport/pgtest"
	"keyloop-coding-challenge/pkg/eventsourcing"
)

type postgresAPI struct {
	srv     *httptest.Server
	admin   *gorm.DB
	store   eventstore.AggregateStore // admin-side store for reloads
	rebuild *projection.Runner        // as the rebuild role
	// connections as the deployed roles, for privilege checks
	app       *gorm.DB
	reader    *gorm.DB
	rebuilder *gorm.DB
}

// newPostgresAPI serves the real handler wired like cmd/api on one PostgreSQL database:
// commands as the application writer (deploy/postgres/app-grants.sql) with the read
// model projected in the same transaction, queries as the read-only role
// (deploy/postgres/grants.sql). appParams are extra connection parameters of the writer.
func newPostgresAPI(t *testing.T, appParams map[string]string) *postgresAPI {
	t.Helper()
	admin, dsn := pgtest.NewDatabase(t)
	if err := repos.Migrate(admin); err != nil {
		t.Fatal(err)
	}
	if err := viewstore.Migrate(admin); err != nil {
		t.Fatal(err)
	}
	app, _ := pgtest.Role(t, admin, dsn, "app-grants.sql", "app", appParams)
	reader, _ := pgtest.Role(t, admin, dsn, "grants.sql", "reader", nil)
	rebuilder, _ := pgtest.Role(t, admin, dsn, "rebuild-grants.sql", "rebuild", nil)

	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&aggregates.VehicleInventoryAggregate{}); err != nil {
		t.Fatal(err)
	}
	store := eventstore.NewAggregateStoreWithSnapshotInterval(
		repos.New(app, serializer),
		0,
		eventstore.WithProjection(projection.NewCommandProjection(&projectors.VehicleProjector{})),
	)
	commands := command.NewService(commandstore.New(store), referencedata.NewDealerships(app), fixedClock{}, discard)
	queries := query.NewService(viewstore.NewReadModel(reader), fixedClock{})
	sqlDB, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(presentation.NewHandler(commands, queries, sqlDB.PingContext, discard))
	t.Cleanup(srv.Close)
	return &postgresAPI{
		srv:   srv,
		admin: admin,
		store: eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(admin, serializer), 0),
		rebuild: projection.NewRunner(
			func(tx *gorm.DB) projection.EventSource { return repos.NewEventStore(tx, serializer) },
			viewstore.NewPostgres(rebuilder),
			&projectors.VehicleProjector{},
		),
		app:       app,
		reader:    reader,
		rebuilder: rebuilder,
	}
}

type eventRow struct {
	ID            int64
	Version       int
	EventType     string
	AggregateType string
	Metadata      string
}

func (m *postgresAPI) events(t *testing.T, id string) []eventRow {
	t.Helper()
	var rows []eventRow
	if err := m.admin.Raw(`SELECT id, version, event_type, aggregate_type, metadata FROM event_store.es_event WHERE aggregate_id = ? ORDER BY version`, id).
		Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func (m *postgresAPI) eventTypes(t *testing.T, id string) []string {
	var types []string
	for _, e := range m.events(t, id) {
		types = append(types, e.EventType)
	}
	return types
}

func (m *postgresAPI) stock(t *testing.T, id, stockedOn string) {
	t.Helper()
	if r := do(t, m.srv, call{method: "POST", path: vehicles, body: stockBody(id, stockedOn)}); r.status != 201 {
		t.Fatalf("stock %s: %d %s", id, r.status, r.rawBody)
	}
}

// projected returns the read model's applied version and history versions/event ids.
func (m *postgresAPI) projected(t *testing.T, id string) (int, []historyKey) {
	t.Helper()
	var version int
	if err := m.admin.Raw(`SELECT COALESCE((SELECT version FROM read_model.projection_aggregate WHERE aggregate_id = ?), 0)`, id).
		Scan(&version).Error; err != nil {
		t.Fatal(err)
	}
	var history []historyKey
	if err := m.admin.Raw(`SELECT version, event_id FROM read_model.vehicle_action_history WHERE vehicle_id = ? ORDER BY version`, id).
		Scan(&history).Error; err != nil {
		t.Fatal(err)
	}
	return version, history
}

type historyKey struct {
	Version int
	EventID int64
}
