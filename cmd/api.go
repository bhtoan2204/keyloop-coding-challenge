package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/application/query"
	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/commandstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/referencedata"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/internal/presentation"
	"keyloop-coding-challenge/pkg/eventsourcing"
)

// vehicleSnapshotInterval is 0: vehicle streams are short (stock, a few action
// changes, sale), so full replay is one indexed range read and snapshots would only
// add writes and stored JSON to keep compatible (docs/04 §7).
const vehicleSnapshotInterval = 0

// runAPI serves the REST API. Commands run as the application writer: one PostgreSQL
// transaction appends the events and updates the read model. Queries run as the
// read-only role.
func runAPI(ctx context.Context, log *slog.Logger) error {
	writer, err := openPostgres(ctx, "PG_APP_DSN", "POSTGRES_APP_USER", "inventory_app", "POSTGRES_APP_PASSWORD")
	if err != nil {
		return err
	}
	writerDB, err := writer.DB()
	if err != nil {
		return err
	}
	defer writerDB.Close()

	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&aggregates.VehicleInventoryAggregate{}); err != nil {
		return err
	}
	store := eventstore.NewAggregateStoreWithSnapshotInterval(
		repos.New(writer, serializer),
		vehicleSnapshotInterval,
		eventstore.WithProjection(projection.NewCommandProjection(&projectors.VehicleProjector{})),
	)
	commands := command.NewService(
		commandstore.New(store),
		referencedata.NewDealerships(writer),
		command.SystemClock{},
		log,
	)

	reader, err := openPostgres(ctx, "PG_READ_DSN", "POSTGRES_READER_USER", "inventory_reader", "POSTGRES_READER_PASSWORD")
	if err != nil {
		return err
	}
	readerDB, err := reader.DB()
	if err != nil {
		return err
	}
	defer readerDB.Close()
	queries := query.NewService(viewstore.NewReadModel(reader), command.SystemClock{})
	ready := func(ctx context.Context) error {
		if err := writerDB.PingContext(ctx); err != nil {
			return err
		}
		return readerDB.PingContext(ctx)
	}

	srv := &http.Server{
		Addr:              envOr("HTTP_ADDR", "127.0.0.1:8080"), // set 0.0.0.0:8080 explicitly in a container
		Handler:           presentation.NewHandler(commands, queries, ready, log),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return serveUntilCanceled(ctx, log, srv)
}
