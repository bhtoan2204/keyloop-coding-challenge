package repos

import (
	_ "embed"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

//go:embed schema.sql
var mysqlSchema string

// Migrate creates the event-store tables.
// MySQL uses schema.sql. Other dialects use the same tables with SQLite DDL.
func Migrate(db *gorm.DB) error {
	script := sqliteSchema
	if db.Dialector.Name() == "mysql" {
		script = mysqlSchema
	}
	for _, stmt := range splitSQL(script) {
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
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
