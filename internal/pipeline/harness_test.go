//go:build integration && pipeline

// Package pipeline_test runs the whole change feed: commands → MySQL es_event →
// Debezium (Kafka Connect) → Kafka → messaging.Consumer → PostgreSQL. Every test gets
// its own MySQL and PostgreSQL databases, its own connector (registered from
// deploy/debezium/es-event-connector.json with per-test names), topic and consumer
// group. Tests fail (never skip) when the infrastructure is not configured.
package pipeline_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/internal/testsupport/mysqltest"
	"keyloop-coding-challenge/internal/testsupport/pgtest"
	"keyloop-coding-challenge/package/eventsourcing"
)

var ctx = context.Background()

// testLog keeps the consumer's logs out of the test output unless PIPELINE_LOG=1.
func testLog() *slog.Logger {
	if os.Getenv("PIPELINE_LOG") == "1" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func requireEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("%s is not set: start the stack with `make up cdc-up debezium-grants` and set it (see .env.example)", key)
	}
	return v
}

// env is one isolated pipeline: databases, connector, topic, consumer group.
type env struct {
	name       string
	mysql      *gorm.DB
	mysqlCfg   *mysql.Config
	pg         *gorm.DB
	pgDSN      string
	serializer eventsourcing.Serializer
	events     repos.EventStore
	brokers    []string
	connectURL string
	connector  string
	topic      string
	group      string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		brokers:    strings.Split(requireEnv(t, "KAFKA_TEST_BROKERS"), ","),
		connectURL: strings.TrimRight(requireEnv(t, "CONNECT_TEST_URL"), "/"),
	}
	e.mysql, e.mysqlCfg = mysqltest.NewMigratedDatabase(t)
	e.name = "it" + mysqltest.RandomHex(t, 5)
	e.connector = e.name + "-cdc"
	e.topic = e.name + "." + e.mysqlCfg.DBName + ".es_event"
	e.group = e.name + "-projector"

	e.pg, e.pgDSN = pgtest.NewDatabase(t)
	if err := viewstore.Migrate(e.pg); err != nil {
		t.Fatal(err)
	}
	e.serializer = eventsourcing.NewSerializer()
	if err := e.serializer.RegisterAggregate(&aggregates.VehicleInventoryAggregate{}); err != nil {
		t.Fatal(err)
	}
	e.events = repos.New(e.mysql, e.serializer).EventStore()

	// The connector's MySQL account (make debezium-grants) may read this database's
	// es_event only, exactly as it may read the real one.
	user := envOr("DEBEZIUM_MYSQL_USER", "debezium")
	grant := fmt.Sprintf("`%s`.`es_event` TO '%s'@'%%'", e.mysqlCfg.DBName, user)
	mysqltest.Exec(t, e.mysqlCfg, "GRANT SELECT ON "+grant)
	t.Cleanup(func() { mysqltest.Exec(t, e.mysqlCfg, "REVOKE SELECT ON "+strings.Replace(grant, " TO ", " FROM ", 1)) })
	t.Cleanup(func() { e.deleteKafkaState(t) })
	return e
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// waitFor polls cond until it holds or the deadline passes. It waits on observable
// state (rows, offsets, connector status), never on a fixed delay.
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
