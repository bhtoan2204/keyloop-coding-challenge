package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"keyloop-coding-challenge/internal/application/messaging"
	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/kafka"
	"keyloop-coding-challenge/internal/infrastructure/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/internal/presentation"
	"keyloop-coding-challenge/package/eventsourcing"
)

// runProjector runs the live projection consumer: the es_event change feed (Debezium
// → Kafka) applied to the PostgreSQL read model, with a startup sync from MySQL and
// gap repair from MySQL (internal/application/messaging). It serves its probes on
// PROJECTOR_HTTP_ADDR and stops gracefully on SIGINT/SIGTERM.
func runProjector(ctx context.Context, log *slog.Logger) error {
	dsn, err := mysqlDSN("MYSQL_DSN", envOr("MYSQL_APP_USER", "inventory_app"), "MYSQL_APP_PASSWORD")
	if err != nil {
		return err
	}
	startupSync, err := strconv.ParseBool(envOr("PROJECTOR_STARTUP_SYNC", "true"))
	if err != nil {
		return fmt.Errorf("PROJECTOR_STARTUP_SYNC: %w", err)
	}
	mysqlDB, err := openMySQL(ctx, dsn)
	if err != nil {
		return err
	}
	if sqlDB, err := mysqlDB.DB(); err == nil {
		defer sqlDB.Close()
	}
	pg, err := openPostgres(ctx, "PG_PROJECTOR_DSN", "POSTGRES_PROJECTOR_USER", "inventory_projector", "POSTGRES_PROJECTOR_PASSWORD")
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

	events := repos.New(mysqlDB, serializer).EventStore()
	store := viewstore.NewPostgres(pg)
	vehicles := &projectors.VehicleProjector{}
	adapter := projection.NewConsumerAdapter(
		projection.NewProjection(store, vehicles),
		projection.NewRunner(events, store, vehicles),
		events,
	)

	// The transport joins the group on its first poll (inside Run), so the listener
	// is filled in before any partition callback can arrive.
	listener := &partitionListener{}
	transport, err := kafka.NewTransport(kafkaConfig(), log, listener)
	if err != nil {
		return err
	}
	cfg := messaging.DefaultConfig()
	cfg.StartupSync = startupSync
	consumer := messaging.NewConsumer(
		transport,
		adapter,
		adapter,
		adapter,
		messaging.NewDecoder(serializer),
		cfg,
		log,
	)
	listener.consumer = consumer

	// The probes and the consumer stop together: a probe server that cannot serve
	// (e.g. the port is taken) stops the consumer instead of leaving it unobservable.
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	probes := &http.Server{
		Addr:              envOr("PROJECTOR_HTTP_ADDR", "127.0.0.1:8081"),
		Handler:           presentation.NewProbeHandler(consumer.Ready, consumer.Status, log),
		ReadHeaderTimeout: 5 * time.Second,
	}
	probeErr := make(chan error, 1)
	go func() {
		err := serveUntilCanceled(runCtx, log, probes)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("probe server failed; stopping the projector", "error", err.Error())
		}
		stop()
		probeErr <- err
	}()

	runErr := consumer.Run(runCtx)
	stop()
	if err := <-probeErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.Join(runErr, err)
	}
	if errors.Is(runErr, context.Canceled) {
		return nil // shutdown during the startup sync
	}
	return runErr
}

func kafkaConfig() kafka.Config {
	return kafka.Config{
		Brokers: splitList(envOr("KAFKA_BROKERS", "127.0.0.1:9092")),
		Topic:   envOr("KAFKA_TOPIC", "inventory.inventory.es_event"),
		Group:   envOr("KAFKA_CONSUMER_GROUP", "inventory-projector"),
	}
}

// splitList splits a comma-separated list, dropping blanks and surrounding spaces.
func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// partitionListener forwards the transport's partition assignment to the consumer's
// health reporting.
type partitionListener struct {
	consumer *messaging.Consumer
}

func (l *partitionListener) AssignPartitions(topic string, partitions []int32) {
	l.consumer.AssignPartitions(topic, partitions)
}

func (l *partitionListener) ForgetPartitions(topic string, partitions []int32) {
	l.consumer.ForgetPartitions(topic, partitions)
}
