//go:build integration

package viewstore_test

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/internal/testsupport/pgtest"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestReaderRoleIsReadOnly applies deploy/postgres/grants.sql and connects as the role
// the API uses: queries work, every write is refused.
func TestReaderRoleIsReadOnly(t *testing.T) {
	db, dsn := pgtest.NewDatabase(t)
	if err := viewstore.Migrate(db); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(dsn)
	dbName := strings.TrimPrefix(u.Path, "/")
	user, password := "rm_reader_"+pgtest.RandomHex(t, 4), pgtest.RandomHex(t, 16)
	script, err := os.ReadFile("../../../deploy/postgres/grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := strings.NewReplacer("{{database}}", dbName, "{{reader_user}}", user, "{{reader_password}}", password).Replace(string(script))
	if err := db.Exec(sqlText).Error; err != nil {
		t.Fatalf("apply grants: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DROP OWNED BY " + user)
		pgtest.Exec(t, os.Getenv("PG_TEST_DSN"), "DROP ROLE IF EXISTS "+user)
	})

	ru := *u
	ru.User = url.UserPassword(user, password)
	reader := pgtest.Open(t, ru.String())
	var n int
	if err := reader.Raw(`SELECT count(*) FROM inventory_vehicle`).Scan(&n).Error; err != nil {
		t.Fatalf("reader cannot query: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO dealership VALUES ('d-x', 'x', 'UTC')`,
		`UPDATE dealership SET name = 'x'`,
		`DELETE FROM inventory_vehicle`,
		`TRUNCATE inventory_vehicle`,
		`SELECT * FROM projection_aggregate`,
		`CREATE TABLE x (id int)`,
	} {
		err := reader.Exec(stmt).Error
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: want insufficient_privilege (42501), got %v", stmt, err)
		}
	}
}
