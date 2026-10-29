//go:build integration

package eventstore_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
	"testing"

	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/package/eventsourcing"

	"github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// With -tags integration every existing AggregateStore test runs against MySQL.
func init() { openDB = openMySQLDB }

// mysqlTestConfig parses MYSQL_TEST_DSN, an admin DSN without a database name.
// A missing DSN fails the test: integration tests must never pass by skipping.
func mysqlTestConfig(t *testing.T) *mysql.Config {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Fatal("MYSQL_TEST_DSN is not set: start MySQL with `make mysql-up` and set it (see .env.example)")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse MYSQL_TEST_DSN: %v", err)
	}
	cfg.ParseTime = true
	return cfg
}

// newMySQLDatabase creates an isolated, empty database for one test and returns a
// privileged (admin) connection to it. The database is dropped when the test ends.
func newMySQLDatabase(t *testing.T) (*gorm.DB, *mysql.Config) {
	t.Helper()
	cfg := mysqlTestConfig(t)
	name := "es_it_" + randomHex(t, 6)
	adminExec(t, cfg, "CREATE DATABASE `"+name+"`")
	t.Cleanup(func() { adminExec(t, cfg, "DROP DATABASE IF EXISTS `"+name+"`") })

	dbCfg := cfg.Clone()
	dbCfg.DBName = name
	return openGorm(t, dbCfg), dbCfg
}

func openGorm(t *testing.T, cfg *mysql.Config) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gormmysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(32)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func openMySQLDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, _ := newMySQLDatabase(t)
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func adminExec(t *testing.T, cfg *mysql.Config, stmt string, args ...interface{}) {
	t.Helper()
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(stmt, args...); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func init() { openRawDB = openRawMySQLDB }

func openRawMySQLDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, _ := newMySQLDatabase(t)
	return db
}

// TestMySQLReadAllUsesOneConsistentSnapshot: an event committed while ReadAll is
// streaming must not appear in that run (it belongs to the next one), and a run never
// sees part of a later commit.
func TestMySQLReadAllUsesOneConsistentSnapshot(t *testing.T) {
	db, serializer, store := newStore(t, 0)
	for _, id := range []string{"a", "b", "c"} {
		agg := newQty(t, id)
		addQty(t, agg, 1)
		mustSave(t, store, agg)
	}
	var seen []string
	err := repos.New(db, serializer).EventStore().ReadAll(context.Background(), 1, func(ev eventsourcing.Event) error {
		seen = append(seen, fmt.Sprintf("%s/%d", ev.AggregateID, ev.Version))
		if len(seen) == 1 {
			// Concurrent writer commits two new events (another connection).
			late := newQty(t, "b2")
			addQty(t, late, 1)
			addQty(t, late, 1)
			mustSave(t, store, late)
			more := mustLoad(t, store, "c")
			addQty(t, more, 1)
			mustSave(t, store, more)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a/1", "b/1", "c/1"}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("saw %v, want only the snapshot %v", seen, want)
	}
}
