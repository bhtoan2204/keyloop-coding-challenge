//go:build integration

package projection_test

import (
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/internal/testsupport/pgtest"
)

// projectorRole applies deploy/postgres/projector-grants.sql for a new role and returns a
// connection as that role: what the projector and the projection commands run as.
func projectorRole(t *testing.T, owner *gorm.DB, dsn string) *gorm.DB {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	user, password := "rm_projector_"+pgtest.RandomHex(t, 4), pgtest.RandomHex(t, 16)
	script, err := os.ReadFile("../../../deploy/postgres/projector-grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := strings.NewReplacer(
		"{{database}}", strings.TrimPrefix(u.Path, "/"),
		"{{projector_user}}", user,
		"{{projector_password}}", password,
	).Replace(string(script))
	if err := owner.Exec(sqlText).Error; err != nil {
		t.Fatalf("apply projector grants: %v", err)
	}
	t.Cleanup(func() {
		owner.Exec("DROP OWNED BY " + user)
		pgtest.Exec(t, os.Getenv("PG_TEST_DSN"), "DROP ROLE IF EXISTS "+user)
	})
	ru := *u
	ru.User = url.UserPassword(user, password)
	return pgtest.Open(t, ru.String())
}

// TestProjectorRoleRunsProjectionsWithoutAdminRights: the restricted role can do
// everything the projector does (rebuild with TRUNCATE, sync, live apply with history
// and version bookkeeping) and nothing an attacker controlling the change feed could
// use to escalate: no schema changes, no reference data, no superuser features.
func TestProjectorRoleRunsProjectionsWithoutAdminRights(t *testing.T) {
	owner, dsn := migratedDB(t)
	role := projectorRole(t, owner, dsn)
	events12 := historyFor(12)

	store := viewstore.NewPostgres(role)
	runner := projection.NewRunner(fakeSource{events: events12[:20]}, store, &projectors.VehicleProjector{})
	if _, err := runner.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild as projector role: %v", err)
	}
	runner = projection.NewRunner(fakeSource{events: events12}, store, &projectors.VehicleProjector{})
	if res, err := runner.Sync(ctx); err != nil || res.Applied != len(events12)-20 {
		t.Fatalf("sync as projector role: %+v %v", res, err)
	}
	var ids int64 = 10_000
	s := newStream("live-1", &ids)
	live := projection.NewProjection(store, &projectors.VehicleProjector{})
	mustApply(t, live,
		s.ev(stocked("d-001", domain.NewDate(2026, 6, 1))),
		s.ev(&events.VehicleActionProposedEvent{ActionID: "a1", ActionType: domain.ActionHold}),
		s.ev(&events.VehicleSoldEvent{SoldOn: domain.NewDate(2026, 10, 1), ClosedActionID: "a1"}),
	)
	if got := []int{appliedVersion(t, owner, "live-1"), len(history(t, owner, "live-1"))}; !reflect.DeepEqual(got, []int{3, 2}) {
		t.Fatalf("live apply: version and history rows %v", got)
	}
	if _, err := projection.NewRunner(fakeSource{events: events12}, store, &projectors.VehicleProjector{}).Rebuild(ctx); err != nil {
		t.Fatalf("second rebuild as projector role: %v", err)
	}

	var super bool
	if err := role.Raw(`SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super).Error; err != nil || super {
		t.Fatalf("projector role is superuser=%v (%v)", super, err)
	}
	for _, stmt := range []string{
		`CREATE TABLE x (id int)`,
		`ALTER TABLE inventory_vehicle ADD COLUMN x int`,
		`DROP TABLE vehicle_action_history`,
		`INSERT INTO dealership VALUES ('d-x', 'x', 'UTC')`,
		`UPDATE dealership SET name = 'x'`,
		`DELETE FROM inventory_vehicle`,
		`DELETE FROM rm_schema_migrations`,
		`INSERT INTO rm_schema_migrations(version, name) VALUES (99, 'x')`,
		`COPY inventory_vehicle TO PROGRAM 'id'`,
		`CREATE ROLE y`,
	} {
		err := role.Exec(stmt).Error
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: want insufficient_privilege (42501), got %v", stmt, err)
		}
	}
}
