package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/pkg/eventsourcing"
)

// runRebuild replaces the read model with a replay of the whole event store, in one
// transaction holding the projection lock exclusively (see projection.Runner.Rebuild).
// It runs as the rebuild role, the only runtime role allowed to TRUNCATE the read
// model. Commands keep the read model current; a rebuild is for recovery, after an
// import, or after a change to a projector.
func runRebuild(ctx context.Context, log *slog.Logger) error {
	pg, err := openPostgres(ctx, "PG_REBUILD_DSN", "POSTGRES_REBUILD_USER", "inventory_rebuild", "POSTGRES_REBUILD_PASSWORD")
	if err != nil {
		return err
	}
	if sqlDB, err := pg.DB(); err == nil {
		defer sqlDB.Close()
	}
	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&aggregates.VehicleInventoryAggregate{}); err != nil {
		return err
	}
	runner := projection.NewRunner(
		func(tx *gorm.DB) projection.EventSource { return repos.NewEventStore(tx, serializer) },
		viewstore.NewPostgres(pg),
		&projectors.VehicleProjector{},
	)
	started := time.Now()
	res, err := runner.Rebuild(ctx)
	if err != nil {
		return fmt.Errorf("rebuild projections: %w", err)
	}
	log.Info("read model rebuilt",
		"events_read", res.Read,
		"events_applied", res.Applied,
		"duration_ms", time.Since(started).Milliseconds())
	return nil
}
