package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openPostgres connects with the URL in dsnVar, or builds one from POSTGRES_HOST,
// POSTGRES_PORT, POSTGRES_DB and the user/password variables given (defaultUser when
// userVar is unset: each role has its own default, never the owner's).
func openPostgres(ctx context.Context, dsnVar, userVar, defaultUser, passwordVar string) (*gorm.DB, error) {
	dsn := os.Getenv(dsnVar)
	if dsn == "" {
		password := os.Getenv(passwordVar)
		if password == "" {
			return nil, fmt.Errorf("set %s or %s", dsnVar, passwordVar)
		}
		u := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(envOr(userVar, defaultUser), password),
			Host:     envOr("POSTGRES_HOST", "127.0.0.1") + ":" + envOr("POSTGRES_PORT", "5432"),
			Path:     "/" + envOr("POSTGRES_DB", "inventory_read"),
			RawQuery: "sslmode=disable&connect_timeout=5",
		}
		dsn = u.String()
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	return db, nil
}
