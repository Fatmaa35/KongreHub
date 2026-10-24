// Package db opens the pgx pool and runs embedded goose migrations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/fatmakaplan/kongrehub/migrations"
)

// Open connects with retries so the API survives a database that starts late
// or restarts (kaos senaryosu).
func Open(ctx context.Context, url string, log *slog.Logger) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 10 * time.Second
	var pool *pgxpool.Pool
	for attempt := 1; attempt <= 30; attempt++ {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err = pool.Ping(pctx)
			cancel()
			if err == nil {
				return pool, nil
			}
			pool.Close()
		}
		log.Warn("veritabanına bağlanılamadı, yeniden denenecek", "attempt", attempt, "err", err.Error())
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(min(attempt, 5)) * time.Second):
		}
	}
	return nil, fmt.Errorf("veritabanına bağlanılamadı: %w", err)
}

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, url string) error {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return err
	}
	sqlDB := stdlib.OpenDB(*cfg)
	defer sqlDB.Close()
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.UpContext(ctx, sqlDB, ".")
}

// SQLDB exposes database/sql for goose-style helpers in tests.
func SQLDB(url string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	return stdlib.OpenDB(*cfg), nil
}

// Querier is satisfied by *pgxpool.Pool, *pgxpool.Conn and pgx.Tx.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// InTx runs fn in a transaction and retries serialization/deadlock failures.
func InTx(ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(tx pgx.Tx) error) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = pgx.BeginTxFunc(ctx, pool, opts, fn)
		if !IsRetryable(err) {
			return err
		}
		time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
	}
	return err
}

// IsRetryable reports serialization failures and deadlocks.
func IsRetryable(err error) bool {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code == "40001" || pe.Code == "40P01"
	}
	return false
}

// IsUniqueViolation reports a unique constraint error (optionally by name).
func IsUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return constraint == "" || pe.ConstraintName == constraint
	}
	return false
}

// IsCheckViolation reports a CHECK / trigger check violation.
func IsCheckViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23514" {
		return constraint == "" || pe.ConstraintName == constraint
	}
	return false
}
