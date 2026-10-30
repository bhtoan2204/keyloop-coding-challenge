package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/package/eventsourcing"
)

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
	pg, err := openPostgres(ctx, "PG_PROJECTOR_DSN", "POSTGRES_PROJECTOR_USER", "inventory_projector", "POSTGRES_PROJECTOR_PASSWORD")
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
