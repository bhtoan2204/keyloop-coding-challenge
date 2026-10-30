package main

import (
	"context"
	"log/slog"

	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
)

// runMigrate applies the event_store then the read_model migrations with the admin
// account (the read model's foreign key needs event_store.dealership).
func runMigrate(ctx context.Context, log *slog.Logger) error {
	pg, err := openPostgres(ctx, "PG_DSN", "POSTGRES_USER", "inventory", "POSTGRES_PASSWORD")
	if err != nil {
		return err
	}
	if sqlDB, err := pg.DB(); err == nil {
		defer sqlDB.Close()
	}
	if err := repos.Migrate(pg.WithContext(ctx)); err != nil {
		return err
	}
	log.Info("event store migrations applied")
	if err := viewstore.Migrate(pg.WithContext(ctx)); err != nil {
		return err
	}
	log.Info("read model migrations applied")
	return nil
}
