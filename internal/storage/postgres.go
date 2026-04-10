package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vymoiseenkov/ai-agents-platform/internal/config"
)

var ErrNotFound = errors.New("provider not found")

type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS providers (
			name             TEXT PRIMARY KEY,
			url              TEXT NOT NULL,
			models           TEXT[] NOT NULL DEFAULT '{}',
			weight           INTEGER NOT NULL DEFAULT 1,
			enabled          BOOLEAN NOT NULL DEFAULT true,
			api_key          TEXT NOT NULL DEFAULT '',
			key_env          TEXT NOT NULL DEFAULT '',
			timeout_seconds  INTEGER NOT NULL DEFAULT 60,
			created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	return err
}

func (s *Store) ListProviders(ctx context.Context) ([]config.Provider, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name, url, models, weight, enabled, api_key, key_env, timeout_seconds
		 FROM providers ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var providers []config.Provider
	for rows.Next() {
		var p config.Provider
		if err := rows.Scan(&p.Name, &p.URL, &p.Models, &p.Weight, &p.Enabled, &p.APIKey, &p.KeyEnv, &p.Timeout); err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, rows.Err()
}

func (s *Store) GetProvider(ctx context.Context, name string) (config.Provider, bool, error) {
	var p config.Provider
	err := s.pool.QueryRow(ctx,
		`SELECT name, url, models, weight, enabled, api_key, key_env, timeout_seconds
		 FROM providers WHERE name = $1`, name).
		Scan(&p.Name, &p.URL, &p.Models, &p.Weight, &p.Enabled, &p.APIKey, &p.KeyEnv, &p.Timeout)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return config.Provider{}, false, nil
		}
		return config.Provider{}, false, err
	}
	return p, true, nil
}

func (s *Store) AddProvider(ctx context.Context, p config.Provider) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO providers (name, url, models, weight, enabled, api_key, key_env, timeout_seconds)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		p.Name, p.URL, p.Models, p.Weight, p.Enabled, p.APIKey, p.KeyEnv, p.Timeout)
	return err
}

func (s *Store) UpdateProvider(ctx context.Context, name string, p config.Provider) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE providers SET url = $2, models = $3, weight = $4, enabled = $5,
		 api_key = $6, key_env = $7, timeout_seconds = $8, updated_at = $9
		 WHERE name = $1`,
		name, p.URL, p.Models, p.Weight, p.Enabled, p.APIKey, p.KeyEnv, p.Timeout, time.Now())
	return err
}

func (s *Store) DeleteProvider(ctx context.Context, name string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM providers WHERE name = $1`, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpsertProvider(ctx context.Context, p config.Provider) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO providers (name, url, models, weight, enabled, api_key, key_env, timeout_seconds)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (name) DO NOTHING`,
		p.Name, p.URL, p.Models, p.Weight, p.Enabled, p.APIKey, p.KeyEnv, p.Timeout)
	return err
}
