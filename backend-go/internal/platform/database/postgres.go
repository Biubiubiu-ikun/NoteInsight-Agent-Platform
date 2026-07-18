package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"creatorinsight/backend-go/internal/config"

	"github.com/XSAM/otelsql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func NewPostgresDB(ctx context.Context, cfg config.PostgresConfig) (*sqlx.DB, error) {
	var (
		sqlDB *sql.DB
		err   error
	)
	if cfg.TracingEnabled {
		sqlDB, err = otelsql.Open("pgx", cfg.DSN,
			otelsql.WithAttributes(attribute.String("db.system.name", "postgresql")),
			otelsql.WithSpanOptions(otelsql.SpanOptions{
				DisableQuery:         true,
				DisableErrSkip:       true,
				OmitConnPrepare:      true,
				OmitRows:             true,
				OmitConnResetSession: true,
				OmitConnectorConnect: true,
				SpanFilter: func(ctx context.Context, _ otelsql.Method, _ string, _ []driver.NamedValue) bool {
					return trace.SpanContextFromContext(ctx).IsValid()
				},
			}),
		)
	} else {
		sqlDB, err = sql.Open("pgx", cfg.DSN)
	}
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db := sqlx.NewDb(sqlDB, "pgx")

	db.SetMaxOpenConns(int(cfg.MaxConns))
	db.SetMaxIdleConns(int(cfg.MaxIdleConns))
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	pingCtx, cancel := context.WithTimeout(ctx, cfg.PingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return db, nil
}
