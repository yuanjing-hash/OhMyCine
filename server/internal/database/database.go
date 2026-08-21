package database

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite"
)

// Open opens the SQLite database with foreign keys, WAL and a busy timeout enabled.
func Open(path string) (*gorm.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}
	if !strings.HasPrefix(path, "file:") && path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	dsn := sqliteDSN(path)
	db, err := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: dsn}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	return db, nil
}

func sqliteDSN(path string) string {
	base, query, _ := strings.Cut(path, "?")
	params := []string{
		"_pragma=foreign_keys(1)",
		"_pragma=busy_timeout(5000)",
		"_txlock=immediate",
	}
	if path == ":memory:" {
		base = "file:ohmycine-memory"
		query = "mode=memory&cache=shared"
	} else {
		if !strings.HasPrefix(base, "file:") {
			base = "file:" + base
		}
		params = append(params, "_pragma=journal_mode(WAL)")
	}
	if query != "" {
		params = append(params, query)
	}
	return base + "?" + strings.Join(params, "&")
}
