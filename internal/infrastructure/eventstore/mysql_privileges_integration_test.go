//go:build integration

package eventstore_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/package/eventsourcing"

	"github.com/go-sql-driver/mysql"
)

// TestMySQLAppUserCannotRewriteHistory applies deploy/mysql/grants.sql to a throwaway
// database and connects as the restricted user it creates.
func TestMySQLAppUserCannotRewriteHistory(t *testing.T) {
	admin, dbCfg := newMySQLDatabase(t)
	if err := repos.Migrate(admin); err != nil {
		t.Fatal(err)
	}

	user, password := "es_app_"+randomHex(t, 4), randomHex(t, 16)
	script, err := os.ReadFile("../../../deploy/mysql/grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := strings.NewReplacer("{{database}}", dbCfg.DBName, "{{app_user}}", user, "{{app_password}}", password).
		Replace(string(script))
	t.Cleanup(func() { adminExec(t, dbCfg, "DROP USER IF EXISTS '"+user+"'@'%'") })
	for _, stmt := range strings.Split(sqlText, ";") {
		if stmt = stripComments(stmt); stmt != "" {
			adminExec(t, dbCfg, stmt)
		}
	}

	appCfg := dbCfg.Clone()
	appCfg.User, appCfg.Passwd = user, password
	app := openGorm(t, appCfg)
	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&qtyAggregate{}); err != nil {
		t.Fatal(err)
	}
	store := newStoreWith(t, app, serializer, 2) // snapshots on: their INSERT is granted too

	// The application path works with the restricted grants.
	agg := newQty(t, "inv")
	addQty(t, agg, 1)
	addQty(t, agg, 1)
	mustSave(t, store, agg)
	loaded := mustLoad(t, store, "inv")
	addQty(t, loaded, 1)
	mustSave(t, store, loaded)
	if final := mustLoad(t, store, "inv"); final.qty != 3 || final.Version() != 3 {
		t.Fatalf("qty=%d version=%d", final.qty, final.Version())
	}

	// History cannot be rewritten, nor the schema changed, by the application user.
	for _, stmt := range []string{
		`UPDATE es_event SET data = '{"delta":100}' WHERE aggregate_id = 'inv'`,
		`DELETE FROM es_event WHERE aggregate_id = 'inv'`,
		`DELETE FROM es_aggregate WHERE id = 'inv'`,
		`TRUNCATE TABLE es_event`,
		`ALTER TABLE es_event ADD COLUMN x INT`,
		`DROP TABLE es_event`,
	} {
		err := app.Exec(stmt).Error
		var myErr *mysql.MySQLError
		// 1142: command denied to user for table.
		if !errors.As(err, &myErr) || myErr.Number != 1142 {
			t.Errorf("%s: want error 1142 (command denied), got %v", stmt, err)
		}
	}
	if v := eventVersions(t, admin, "inv"); len(v) != 3 {
		t.Fatalf("history changed: %v", v)
	}
}

func stripComments(stmt string) string {
	var lines []string
	for _, line := range strings.Split(stmt, "\n") {
		if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "--") {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, " ")
}
