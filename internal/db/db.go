package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return db, nil
}

func Migrate(db *sql.DB, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return execStatements(db, string(raw))
}

func MigrateDir(db *sql.DB, dir string) error {
	if err := ensureMigrationsTable(db); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}

	paths, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(paths)

	if err := seedLegacyMigrations(db, paths); err != nil {
		return fmt.Errorf("seed legacy migrations: %w", err)
	}

	for _, path := range paths {
		name := filepath.Base(path)
		applied, err := migrationApplied(db, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := Migrate(db, path); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := markMigrationApplied(db, name); err != nil {
			return fmt.Errorf("%s: record applied: %w", name, err)
		}
	}
	return nil
}

func ensureMigrationsTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name VARCHAR(255) NOT NULL,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (name)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`)
	return err
}

func migrationApplied(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&n)
	return n > 0, err
}

func markMigrationApplied(db *sql.DB, name string) error {
	_, err := db.Exec(`INSERT INTO schema_migrations (name) VALUES (?)`, name)
	return err
}

// seedLegacyMigrations marks migrations as applied when their schema changes
// already exist (e.g. databases migrated before schema_migrations existed, or
// when MIGRATIONS_PATH previously pointed at a single file).
func seedLegacyMigrations(db *sql.DB, paths []string) error {
	legacy := map[string]struct {
		table  string
		column string
		index  string
	}{
		"003_spot_collect.sql":           {table: "spots", column: "collect"},
		"004_spot_schedule.sql":          {table: "spots", column: "collect_interval_min"},
		"006_wind_humidity_pressure.sql": {table: "wind_data", column: "humidity"},
		"007_windguru_forecast.sql":           {table: "spots", column: "windguru_id"},
		"008_wind_forecast_windguru_id.sql": {table: "wind_forecast", column: "windguru_id"},
		"010_wind_data_unique.sql":        {index: "uk_wind_data_period_location"},
	}
	for _, path := range paths {
		name := filepath.Base(path)
		spec, ok := legacy[name]
		if !ok {
			continue
		}
		applied, err := migrationApplied(db, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if spec.index != "" {
			exists, err := indexExists(db, spec.index)
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
		} else {
			exists, err := columnExists(db, spec.table, spec.column)
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
		}
		if err := markMigrationApplied(db, name); err != nil {
			return err
		}
	}

	hasWGRating, err := tableExists(db, "wind_wg_raiting")
	if err != nil {
		return err
	}
	if hasWGRating {
		if applied, err := migrationApplied(db, "013_wg_rating.sql"); err != nil {
			return err
		} else if !applied {
			if err := markMigrationApplied(db, "013_wg_rating.sql"); err != nil {
				return err
			}
		}
	}

	hasSpots, err := tableExists(db, "spots")
	if err != nil {
		return err
	}
	if !hasSpots {
		return nil
	}
	for _, path := range paths {
		name := filepath.Base(path)
		if !strings.HasPrefix(name, "001_") && !strings.HasPrefix(name, "002_") {
			continue
		}
		applied, err := migrationApplied(db, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := markMigrationApplied(db, name); err != nil {
			return err
		}
	}
	return nil
}

func tableExists(db *sql.DB, table string) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM information_schema.TABLES
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`,
		table,
	).Scan(&n)
	return n > 0, err
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM information_schema.COLUMNS
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
		table, column,
	).Scan(&n)
	return n > 0, err
}

func indexExists(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM information_schema.STATISTICS
		 WHERE TABLE_SCHEMA = DATABASE() AND INDEX_NAME = ?`,
		name,
	).Scan(&n)
	return n > 0, err
}

func execStatements(db *sql.DB, raw string) error {
	for _, stmt := range splitSQL(raw) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate statement failed: %w\nSQL: %s", err, stmt)
		}
	}
	return nil
}

func splitSQL(s string) []string {
	parts := strings.Split(s, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
