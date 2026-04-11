package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vymoiseenkov/ai-agents-platform/internal/a2a"
)

// Agent DB migration — called from Store.migrate()
const agentsMigration = `
CREATE TABLE IF NOT EXISTS agents (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	description   TEXT NOT NULL DEFAULT '',
	url           TEXT NOT NULL,
	version       TEXT NOT NULL DEFAULT '1.0.0',
	capabilities  JSONB NOT NULL DEFAULT '{}',
	skills        JSONB NOT NULL DEFAULT '[]',
	auth_schemes  JSONB NOT NULL DEFAULT '[]',
	provider_name TEXT NOT NULL DEFAULT '',
	status        TEXT NOT NULL DEFAULT 'active',
	embedding     DOUBLE PRECISION[] DEFAULT NULL,
	created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
)`

const agentsMigrationV2 = `
ALTER TABLE agents ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE agents ADD COLUMN IF NOT EXISTS embedding DOUBLE PRECISION[] DEFAULT NULL;
`

func (s *Store) migrateAgents(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, agentsMigration); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, agentsMigrationV2)
	return err
}

func (s *Store) ListAgents(ctx context.Context) ([]a2a.AgentCard, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, description, url, version, capabilities, skills, auth_schemes, provider_name, status, embedding
		 FROM agents ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []a2a.AgentCard
	for rows.Next() {
		a, err := scanAgent(rows.Scan)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *Store) GetAgent(ctx context.Context, id string) (a2a.AgentCard, bool, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, name, description, url, version, capabilities, skills, auth_schemes, provider_name, status, embedding
		 FROM agents WHERE id = $1`, id)
	a, err := scanAgent(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return a2a.AgentCard{}, false, nil
		}
		return a2a.AgentCard{}, false, err
	}
	return a, true, nil
}

func marshalAgentJSON(agent a2a.AgentCard) (capJSON, skillsJSON, authJSON []byte) {
	capJSON, _ = json.Marshal(agent.Capabilities)
	skillsJSON, _ = json.Marshal(agent.Skills)
	authJSON, _ = json.Marshal(agent.Authentication)
	return
}

func (s *Store) AddAgent(ctx context.Context, agent a2a.AgentCard) error {
	capJSON, skillsJSON, authJSON := marshalAgentJSON(agent)
	status := agent.Status
	if status == "" {
		status = a2a.StatusActive
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO agents (id, name, description, url, version, capabilities, skills, auth_schemes, provider_name, status, embedding)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		agent.ID, agent.Name, agent.Description, agent.URL, agent.Version,
		capJSON, skillsJSON, authJSON, agent.ProviderName, status, agent.Embedding)
	return err
}

func (s *Store) UpdateAgent(ctx context.Context, id string, agent a2a.AgentCard) error {
	capJSON, skillsJSON, authJSON := marshalAgentJSON(agent)
	status := agent.Status
	if status == "" {
		status = a2a.StatusActive
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE agents SET name = $2, description = $3, url = $4, version = $5,
		 capabilities = $6, skills = $7, auth_schemes = $8, provider_name = $9,
		 status = $10, embedding = $11, updated_at = $12
		 WHERE id = $1`,
		id, agent.Name, agent.Description, agent.URL, agent.Version,
		capJSON, skillsJSON, authJSON, agent.ProviderName, status, agent.Embedding, time.Now())
	return err
}

func (s *Store) UpdateAgentStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE agents SET status = $2, updated_at = $3 WHERE id = $1`,
		id, status, time.Now())
	return err
}

func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM agents WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanAgent(scan func(dest ...any) error) (a2a.AgentCard, error) {
	var a a2a.AgentCard
	var capJSON, skillsJSON, authJSON []byte

	err := scan(&a.ID, &a.Name, &a.Description, &a.URL, &a.Version,
		&capJSON, &skillsJSON, &authJSON, &a.ProviderName, &a.Status, &a.Embedding)
	if err != nil {
		return a, err
	}

	json.Unmarshal(capJSON, &a.Capabilities)
	json.Unmarshal(skillsJSON, &a.Skills)

	var auth a2a.Authentication
	if json.Unmarshal(authJSON, &auth) == nil {
		a.Authentication = &auth
	}

	if a.Status == "" {
		a.Status = a2a.StatusActive
	}

	return a, nil
}
