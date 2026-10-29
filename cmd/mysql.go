package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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
