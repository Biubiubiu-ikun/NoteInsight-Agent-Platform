package migration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
)

type Result struct {
	Applied []string
	Skipped []string
}

func Apply(ctx context.Context, db *sqlx.DB, dir string) (Result, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Result{}, fmt.Errorf("read migrations dir: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)

	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`); err != nil {
		return Result{}, fmt.Errorf("ensure schema_migrations: %w", err)
	}

	result := Result{}
	for _, file := range files {
		applied, err := isApplied(ctx, db, file)
		if err != nil {
			return Result{}, err
		}
		if applied {
			result.Skipped = append(result.Skipped, file)
			continue
		}

		sqlBytes, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			return Result{}, fmt.Errorf("read migration %s: %w", file, err)
		}

		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return Result{}, fmt.Errorf("begin migration %s: %w", file, err)
		}

		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return Result{}, fmt.Errorf("execute migration %s: %w", file, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, file); err != nil {
			_ = tx.Rollback()
			return Result{}, fmt.Errorf("record migration %s: %w", file, err)
		}
		if err := tx.Commit(); err != nil {
			return Result{}, fmt.Errorf("commit migration %s: %w", file, err)
		}

		result.Applied = append(result.Applied, file)
	}

	return result, nil
}

func isApplied(ctx context.Context, db *sqlx.DB, version string) (bool, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&exists); err != nil {
		return false, fmt.Errorf("check migration %s: %w", version, err)
	}
	return exists, nil
}
