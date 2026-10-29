//go:build integration

// Package mysqltest provides isolated MySQL databases for integration tests outside
// the eventstore package. Tests fail (never skip) when MYSQL_TEST_DSN is not set.
package mysqltest

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"

	"github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NewMigratedDatabase creates a throwaway database, migrates it and returns an admin
// connection plus its config. The database is dropped when the test ends.
func NewMigratedDatabase(t *testing.T) (*gorm.DB, *mysql.Config) {
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
	name := "es_it_" + RandomHex(t, 6)
	Exec(t, cfg, "CREATE DATABASE `"+name+"`")
	t.Cleanup(func() { Exec(t, cfg, "DROP DATABASE IF EXISTS `"+name+"`") })

	dbCfg := cfg.Clone()
	dbCfg.DBName = name
	db := Open(t, dbCfg)
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db, dbCfg
}

// AppUser applies deploy/mysql/grants.sql for a new random user and returns that
// user's config: the restricted account the API runs with.
func AppUser(t *testing.T, admin *mysql.Config) *mysql.Config {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	script, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../deploy/mysql/grants.sql"))
	if err != nil {
		t.Fatal(err)
	}
	user, password := "es_app_"+RandomHex(t, 4), RandomHex(t, 16)
	sqlText := strings.NewReplacer("{{database}}", admin.DBName, "{{app_user}}", user, "{{app_password}}", password).
		Replace(string(script))
	t.Cleanup(func() { Exec(t, admin, "DROP USER IF EXISTS '"+user+"'@'%'") })
	for _, stmt := range strings.Split(sqlText, ";") {
		var lines []string
		for _, line := range strings.Split(stmt, "\n") {
			if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "--") {
				lines = append(lines, l)
			}
		}
		if len(lines) > 0 {
			Exec(t, admin, strings.Join(lines, " "))
		}
	}
	app := admin.Clone()
	app.User, app.Passwd = user, password
	return app
}

func Open(t *testing.T, cfg *mysql.Config) *gorm.DB {
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

func Exec(t *testing.T, cfg *mysql.Config, stmt string) {
	t.Helper()
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(stmt); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}

func RandomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
