package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
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

	conn, err := db.Connx(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext('noteinsight_schema_migrations'))`); err != nil {
		return Result{}, fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext('noteinsight_schema_migrations'))`)
	}()

	if _, err := conn.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
	checksum TEXT,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`); err != nil {
		return Result{}, fmt.Errorf("ensure schema_migrations: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum TEXT`); err != nil {
		return Result{}, fmt.Errorf("ensure migration checksums: %w", err)
	}

	result := Result{}
	for _, file := range files {
		sqlBytes, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			return Result{}, fmt.Errorf("read migration %s: %w", file, err)
		}
		checksum := migrationChecksum(sqlBytes)
		applied, storedChecksum, err := migrationStatus(ctx, conn, file)
		if err != nil {
			return Result{}, err
		}
		if applied {
			if storedChecksum.Valid && storedChecksum.String != checksum {
				return Result{}, fmt.Errorf("migration %s checksum mismatch", file)
			}
			if !storedChecksum.Valid || storedChecksum.String == "" {
				if _, err := conn.ExecContext(ctx, `UPDATE schema_migrations SET checksum = $2 WHERE version = $1`, file, checksum); err != nil {
					return Result{}, fmt.Errorf("backfill migration %s checksum: %w", file, err)
				}
			}
			result.Skipped = append(result.Skipped, file)
			continue
		}

		tx, err := conn.BeginTxx(ctx, nil)
		if err != nil {
			return Result{}, fmt.Errorf("begin migration %s: %w", file, err)
		}

		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return Result{}, fmt.Errorf("execute migration %s: %w", file, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`, file, checksum); err != nil {
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

func migrationStatus(ctx context.Context, conn *sqlx.Conn, version string) (bool, sql.NullString, error) {
	var checksum sql.NullString
	err := conn.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version = $1`, version).Scan(&checksum)
	if err == sql.ErrNoRows {
		return false, sql.NullString{}, nil
	}
	if err != nil {
		return false, sql.NullString{}, fmt.Errorf("check migration %s: %w", version, err)
	}
	return true, checksum, nil
}

func migrationChecksum(contents []byte) string {
	digest := sha256.Sum256(contents)
	return fmt.Sprintf("%x", digest[:])
}
