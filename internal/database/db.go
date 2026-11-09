package database

import (
	"context"
	"fmt"
	"time"

	"Transcoder/pkg/config"
	"Transcoder/pkg/logger"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewDatabase(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	log := logger.NewLogger("DB")

	poolConfig, err := pgxpool.ParseConfig(cfg.DB.ConnectionString)
	if err != nil {
		return nil, fmt.Errorf("parse database connection string: %w", err)
	}
	if cfg.DB.MaxConnections > 0 {
		poolConfig.MaxConns = int32(cfg.DB.MaxConnections)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	log.Infof(
		"connected to PostgreSQL database %q (max_connections=%d)",
		poolConfig.ConnConfig.Database,
		poolConfig.MaxConns,
	)

	return pool, nil
}
