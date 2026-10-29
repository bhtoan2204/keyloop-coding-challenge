//go:build integration

// Package pgtest provides isolated PostgreSQL databases for integration tests. Tests
// fail (never skip) when PG_TEST_DSN is not set.
package pgtest

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NewDatabase creates a throwaway, empty database and returns an owner connection and
// its URL. The database is dropped afterwards. Callers apply viewstore.Migrate (this
// package does not import viewstore, so viewstore's own tests can use it).
func NewDatabase(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	admin := os.Getenv("PG_TEST_DSN")
	if admin == "" {
		t.Fatal("PG_TEST_DSN is not set: start PostgreSQL with `make pg-up` and set it (see .env.example)")
	}
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatalf("parse PG_TEST_DSN: %v", err)
	}
	name := "rm_it_" + RandomHex(t, 6)
	Exec(t, admin, "CREATE DATABASE "+name)
	t.Cleanup(func() { Exec(t, admin, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })
	u.Path = "/" + name
	dsn := u.String()
	return Open(t, dsn), dsn
}

func Open(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
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

func Exec(t *testing.T, dsn, stmt string) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
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
