//go:build integration

package pgtest

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// Role applies deploy/postgres/<script> for a new random role of the given kind (its
// placeholders are {{database}}, {{<kind>_user}} and {{<kind>_password}}) and returns a
// connection as that role plus its URL. extra are connection parameters (e.g.
// lock_timeout). The role is dropped when the test ends.
func Role(t *testing.T, admin *gorm.DB, dsn, script, kind string, extra map[string]string) (*gorm.DB, string) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../deploy/postgres", script))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	user, password := "rm_"+kind+"_"+RandomHex(t, 4), RandomHex(t, 16)
	sqlText := strings.NewReplacer(
		"{{database}}", strings.TrimPrefix(u.Path, "/"),
		"{{"+kind+"_user}}", user,
		"{{"+kind+"_password}}", password,
	).Replace(string(raw))
	if err := admin.Exec(sqlText).Error; err != nil {
		t.Fatalf("apply %s: %v", script, err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP OWNED BY " + user).Error; err != nil {
			t.Logf("drop owned by %s: %v", user, err)
		}
		Exec(t, os.Getenv("PG_TEST_DSN"), "DROP ROLE IF EXISTS "+user)
	})
	ru := *u
	ru.User = url.UserPassword(user, password)
	q := ru.Query()
	for k, v := range extra {
		q.Set(k, v)
	}
	ru.RawQuery = q.Encode()
	return Open(t, ru.String()), ru.String()
}
