package main

import (
	"context"
	"log/slog"

	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
)

func runMigrate(ctx context.Context, log *slog.Logger) error {
	dsn, err := mysqlDSN("MYSQL_MIGRATE_DSN", "root", "MYSQL_ROOT_PASSWORD")
	if err != nil {
		return err
	}
	db, err := openMySQL(ctx, dsn)
	if err != nil {
		return err
	}
	if err := repos.Migrate(db.WithContext(ctx)); err != nil {
		return err
	}
	log.Info("event store migrations applied")
	pg, err := openPostgres(ctx, "PG_DSN", "POSTGRES_USER", "inventory", "POSTGRES_PASSWORD")
	if err != nil {
		return err
	}
	if err := viewstore.Migrate(pg.WithContext(ctx)); err != nil {
		return err
	}
	log.Info("read model migrations applied")
	return nil
}
