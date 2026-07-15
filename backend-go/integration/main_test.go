//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/platform/migration"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

var (
	integrationDB *sqlx.DB
	systemDSN     string
)

func TestMain(m *testing.M) {
	baseDSN := os.Getenv("POSTGRES_DSN")
	if baseDSN == "" {
		baseDSN = "postgres://creatorinsight:creatorinsight@127.0.0.1:15432/creatorinsight?sslmode=disable"
	}
	systemDSN = baseDSN
	parsed, err := url.Parse(baseDSN)
	if err != nil || parsed.Scheme == "" {
		fmt.Fprintf(os.Stderr, "integration POSTGRES_DSN must be a URL: %v\n", err)
		os.Exit(1)
	}
	databaseName := fmt.Sprintf("noteinsight_it_%d", time.Now().UnixNano())
	adminURL := *parsed
	adminURL.Path = "/postgres"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	adminDB, err := sqlx.ConnectContext(ctx, "pgx", adminURL.String())
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect integration admin database: %v\n", err)
		os.Exit(1)
	}
	if _, err := adminDB.ExecContext(ctx, `CREATE DATABASE `+databaseName); err != nil {
		fmt.Fprintf(os.Stderr, "create integration database: %v\n", err)
		_ = adminDB.Close()
		os.Exit(1)
	}

	testURL := *parsed
	testURL.Path = "/" + databaseName
	integrationDB, err = sqlx.ConnectContext(ctx, "pgx", testURL.String())
	if err == nil {
		_, err = migration.Apply(ctx, integrationDB, filepath.Join("..", "migrations"))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare integration database: %v\n", err)
		_ = integrationDB.Close()
		_, _ = adminDB.ExecContext(context.Background(), `DROP DATABASE `+databaseName+` WITH (FORCE)`)
		_ = adminDB.Close()
		os.Exit(1)
	}

	code := m.Run()
	_ = integrationDB.Close()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	_, dropErr := adminDB.ExecContext(cleanupCtx, `DROP DATABASE `+databaseName+` WITH (FORCE)`)
	cleanupCancel()
	_ = adminDB.Close()
	if dropErr != nil {
		fmt.Fprintf(os.Stderr, "drop integration database: %v\n", dropErr)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
