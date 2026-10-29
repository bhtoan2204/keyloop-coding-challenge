package repos

import (
	_ "embed"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

//go:embed schema.sql
var mysqlSchema string

// migration is one schema version. apply must be idempotent: MySQL DDL is not
// transactional, so a run that stopped after some statements is completed by running
// it again.
type migration struct {
	version int
	name    string
	apply   func(db *gorm.DB) error
}

var migrations = []migration{
	{1, "event store tables", applyBaseSchema},
	{2, "es_event aggregate_type and schema_version", addEventStreamColumns},
}

// Migrate brings the event-store schema up to date. A database created by the original
// unversioned Migrate is adopted: version 1 is that same CREATE TABLE IF NOT EXISTS DDL.
// Applied versions are recorded in es_schema_migrations. On MySQL concurrent callers are
// serialised with a per-database named lock.
func Migrate(db *gorm.DB) error {
	// One pinned connection, so the MySQL named lock is held by the session that migrates.
	return db.Connection(func(conn *gorm.DB) error {
		if isMySQL(conn) {
			var got int
			// Lock names are limited to 64 characters; hash the database name.
			if err := conn.Raw(`SELECT GET_LOCK(CONCAT('es_migrate:', MD5(DATABASE())), 60)`).Scan(&got).Error; err != nil {
				return fmt.Errorf("migrate: acquire lock: %w", err)
			}
			if got != 1 {
				return fmt.Errorf("migrate: timed out waiting for the migration lock")
			}
			defer conn.Exec(`SELECT RELEASE_LOCK(CONCAT('es_migrate:', MD5(DATABASE())))`)
		}
		return migrate(conn)
	})
}

func migrate(db *gorm.DB) error {
	ddl := `CREATE TABLE IF NOT EXISTS es_schema_migrations (
		version INTEGER NOT NULL PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`
	if isMySQL(db) {
		ddl = `CREATE TABLE IF NOT EXISTS es_schema_migrations (
			version INT NOT NULL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`
	}
	if err := db.Exec(ddl).Error; err != nil {
		return fmt.Errorf("migrate: create es_schema_migrations: %w", err)
	}
	var applied []int
	if err := db.Raw(`SELECT version FROM es_schema_migrations`).Scan(&applied).Error; err != nil {
		return fmt.Errorf("migrate: read applied versions: %w", err)
	}
	latest := migrations[len(migrations)-1].version
	done := make(map[int]bool, len(applied))
	for _, v := range applied {
		if v > latest {
			// An older release must not run against a schema it does not know.
			return fmt.Errorf("migrate: database has schema version %d, this build knows up to %d", v, latest)
		}
		done[v] = true
	}
	for _, m := range migrations {
		if done[m.version] {
			continue
		}
		if err := m.apply(db); err != nil {
			return fmt.Errorf("migrate: version %d (%s): %w", m.version, m.name, err)
		}
		if err := db.Exec(`INSERT INTO es_schema_migrations(version, name) VALUES(?, ?)`, m.version, m.name).Error; err != nil {
			return fmt.Errorf("migrate: record version %d: %w", m.version, err)
		}
	}
	return nil
}

// applyBaseSchema is the original schema: schema.sql on MySQL, sqliteSchema in tests.
func applyBaseSchema(db *gorm.DB) error {
	script := sqliteSchema
	if isMySQL(db) {
		script = mysqlSchema
	}
	for _, stmt := range splitSQL(script) {
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

// addEventStreamColumns makes each es_event row self-describing for CDC consumers:
// aggregate_type (backfilled from es_aggregate for existing rows) and the payload
// schema_version (1 for every existing row). Both are additive with defaults, so the
// original columns, data and readers are unaffected.
func addEventStreamColumns(db *gorm.DB) error {
	columns := []struct{ name, mysql, sqlite string }{
		{"aggregate_type", "VARCHAR(255) NOT NULL DEFAULT ''", "TEXT NOT NULL DEFAULT ''"},
		{"schema_version", "INT NOT NULL DEFAULT 1", "INTEGER NOT NULL DEFAULT 1"},
	}
	for _, c := range columns {
		exists, err := columnExists(db, "es_event", c.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		def := c.sqlite
		if isMySQL(db) {
			def = c.mysql
		}
		if err := db.Exec(fmt.Sprintf("ALTER TABLE es_event ADD COLUMN %s %s", c.name, def)).Error; err != nil {
			return err
		}
	}
	return db.Exec(`UPDATE es_event
		SET aggregate_type = (SELECT a.aggregate_type FROM es_aggregate a WHERE a.id = es_event.aggregate_id)
		WHERE aggregate_type = ''
			AND EXISTS (SELECT 1 FROM es_aggregate a WHERE a.id = es_event.aggregate_id)`).Error
}

func columnExists(db *gorm.DB, table, column string) (bool, error) {
	query := `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`
	if isMySQL(db) {
		query = `SELECT COUNT(*) FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`
	}
	var n int
	if err := db.Raw(query, table, column).Scan(&n).Error; err != nil {
		return false, fmt.Errorf("inspect %s.%s: %w", table, column, err)
	}
	return n > 0, nil
}

func isMySQL(db *gorm.DB) bool {
	return db.Dialector.Name() == "mysql"
}

func splitSQL(script string) []string {
	var b strings.Builder
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	parts := strings.Split(b.String(), ";")
	stmts := make([]string, 0, len(parts))
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt != "" {
			stmts = append(stmts, stmt)
		}
	}
	return stmts
}

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS es_aggregate (
	id TEXT NOT NULL PRIMARY KEY,
	version INTEGER NOT NULL,
	aggregate_type TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS es_event (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	aggregate_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	version INTEGER NOT NULL,
	data TEXT NOT NULL,
	metadata TEXT,
	created_at INTEGER NOT NULL,
	UNIQUE (aggregate_id, version)
);

CREATE TABLE IF NOT EXISTS es_aggregate_snapshot (
	aggregate_id TEXT NOT NULL,
	version INTEGER NOT NULL,
	data TEXT NOT NULL,
	PRIMARY KEY (aggregate_id, version)
);
`
