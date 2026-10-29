// Command main runs the inventory service.
//
//	main migrate   apply event-store migrations (admin MySQL account)
//	main api       serve the REST write API (restricted application account)
//
// Configuration comes from the environment; see .env.example.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // dealership time zones must resolve in minimal containers too

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/commandstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/referencedata"
	"keyloop-coding-challenge/internal/presentation"
	"keyloop-coding-challenge/package/eventsourcing"

	"github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// vehicleSnapshotInterval is 0: vehicle streams are short (stock, a few action
// changes, sale), so full replay is one indexed range read and snapshots would only
// add writes and stored JSON to keep compatible (docs/04 §7).
const vehicleSnapshotInterval = 0

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel()}))
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: main api|migrate")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch args[0] {
	case "api":
		err = runAPI(ctx, log)
	case "migrate":
		err = runMigrate(ctx, log)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q; usage: main api|migrate\n", args[0])
		return 2
	}
	if err != nil {
		log.Error("exiting", "command", args[0], "error", err.Error())
		return 1
	}
	return 0
}

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
	log.Info("migrations applied")
	return nil
}

func runAPI(ctx context.Context, log *slog.Logger) error {
	dsn, err := mysqlDSN("MYSQL_DSN", envOr("MYSQL_APP_USER", "inventory_app"), "MYSQL_APP_PASSWORD")
	if err != nil {
		return err
	}
	db, err := openMySQL(ctx, dsn)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&aggregates.VehicleInventoryAggregate{}); err != nil {
		return err
	}
	store := eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(db, serializer), vehicleSnapshotInterval)
	commands := command.NewService(commandstore.New(store), referencedata.NewDealerships(db), command.SystemClock{}, log)

	srv := &http.Server{
		Addr:              envOr("HTTP_ADDR", "127.0.0.1:8080"), // set 0.0.0.0:8080 explicitly in a container
		Handler:           presentation.NewHandler(commands, sqlDB.PingContext, log),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", srv.Addr)
		errs <- srv.ListenAndServe()
	}()
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-errs; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("stopped")
	return nil
}

// mysqlDSN returns the DSN in dsnVar, or builds one from MYSQL_HOST, MYSQL_PORT,
// MYSQL_DATABASE, the given user and the password in passwordVar.
func mysqlDSN(dsnVar, user, passwordVar string) (string, error) {
	cfg := mysql.NewConfig()
	if raw := os.Getenv(dsnVar); raw != "" {
		parsed, err := mysql.ParseDSN(raw)
		if err != nil {
			return "", fmt.Errorf("%s: %w", dsnVar, err)
		}
		cfg = parsed
	} else {
		password := os.Getenv(passwordVar)
		if password == "" {
			return "", fmt.Errorf("set %s or %s", dsnVar, passwordVar)
		}
		cfg.User, cfg.Passwd = user, password
		cfg.Net = "tcp"
		cfg.Addr = envOr("MYSQL_HOST", "127.0.0.1") + ":" + envOr("MYSQL_PORT", "3306")
		cfg.DBName = envOr("MYSQL_DATABASE", "inventory")
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	return cfg.FormatDSN(), nil
}

func openMySQL(ctx context.Context, dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
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
		return nil, fmt.Errorf("connect mysql: %w", err)
	}
	return db, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func logLevel() slog.Level {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}
