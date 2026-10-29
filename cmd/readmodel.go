package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"time"

	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const usage = "usage: main api|migrate|sync-projections|rebuild-projections"

// runProjections brings the PostgreSQL read model up to date from the MySQL event
// store, reading the whole history from one consistent snapshot (see
// projection.Runner). It is a point-in-time catch-up, not a live feed: in Phase 4 the
// read model only changes when this command runs. One run at a time is enforced by a
// PostgreSQL advisory lock; a concurrent run fails fast.
func runProjections(ctx context.Context, log *slog.Logger, rebuild bool) error {
	dsn, err := mysqlDSN("MYSQL_DSN", envOr("MYSQL_APP_USER", "inventory_app"), "MYSQL_APP_PASSWORD")
	if err != nil {
		return err
	}
	mysqlDB, err := openMySQL(ctx, dsn)
	if err != nil {
		return err
	}
	pg, err := openPostgres(ctx, "PG_DSN", "POSTGRES_USER", "inventory", "POSTGRES_PASSWORD")
	if err != nil {
		return err
	}
	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&aggregates.VehicleInventoryAggregate{}); err != nil {
		return err
	}
	runner := projection.NewRunner(repos.New(mysqlDB, serializer).EventStore(), viewstore.NewPostgres(pg),
		&projectors.VehicleProjector{})

	kind, run := "sync", runner.Sync
	if rebuild {
		kind, run = "rebuild", runner.Rebuild
	}
	started := time.Now()
	res, err := run(ctx)
	if err != nil {
		return fmt.Errorf("%s projections: %w", kind, err)
	}
	log.Info("projections up to date", "kind", kind, "events_read", res.Read, "events_applied", res.Applied,
		"duration_ms", time.Since(started).Milliseconds())
	return nil
}

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
