package a2a

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"sync"
)

// Store defines the persistence interface for agents.
type Store interface {
	ListAgents(ctx context.Context) ([]AgentCard, error)
	GetAgent(ctx context.Context, id string) (AgentCard, bool, error)
	AddAgent(ctx context.Context, agent AgentCard) error
	UpdateAgent(ctx context.Context, id string, agent AgentCard) error
	UpdateAgentStatus(ctx context.Context, id, status string) error
	DeleteAgent(ctx context.Context, id string) error
}

// DiscoverResult holds an agent and its similarity score.
type DiscoverResult struct {
	Agent AgentCard `json:"agent"`
	Score float64   `json:"score"`
}

// Registry manages agent cards with in-memory cache backed by persistent storage.
type Registry struct {
	mu       sync.RWMutex
	agents   map[string]AgentCard
	store    Store
	embedder *Embedder
	logger   *slog.Logger
}

func NewRegistry(store Store, embedder *Embedder, logger *slog.Logger) *Registry {
	return &Registry{
		agents:   make(map[string]AgentCard),
		store:    store,
		embedder: embedder,
		logger:   logger,
	}
}

// LoadFromDB populates the in-memory cache from the database.
func (r *Registry) LoadFromDB(ctx context.Context) error {
	agents, err := r.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.agents = make(map[string]AgentCard, len(agents))
	for _, a := range agents {
		r.agents[a.ID] = a
	}
	r.mu.Unlock()
	return nil
}

func (r *Registry) List() []AgentCard {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]AgentCard, 0, len(r.agents))
	for _, a := range r.agents {
		result = append(result, a)
	}
	return result
}

func (r *Registry) Get(id string) (AgentCard, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[id]
	return a, ok
}

func (r *Registry) Add(ctx context.Context, agent AgentCard) error {
	if agent.Status == "" {
		agent.Status = StatusActive
	}

	if r.embedder != nil {
		text := BuildEmbeddingText(agent)
		vec, err := r.embedder.Embed(ctx, text)
		if err != nil {
			r.logger.Warn("failed to compute embedding for agent, saving without", "agent", agent.ID, "error", err)
		} else {
			agent.Embedding = vec
		}
	}

	if err := r.store.AddAgent(ctx, agent); err != nil {
		return err
	}
	r.mu.Lock()
	r.agents[agent.ID] = agent
	r.mu.Unlock()
	return nil
}

func (r *Registry) Update(ctx context.Context, id string, agent AgentCard) error {
	if agent.Status == "" {
		agent.Status = StatusActive
	}

	if r.embedder != nil {
		text := BuildEmbeddingText(agent)
		vec, err := r.embedder.Embed(ctx, text)
		if err != nil {
			r.logger.Warn("failed to recompute embedding for agent", "agent", id, "error", err)
			r.mu.RLock()
			if old, ok := r.agents[id]; ok {
				agent.Embedding = old.Embedding
			}
			r.mu.RUnlock()
		} else {
			agent.Embedding = vec
		}
	}

	if err := r.store.UpdateAgent(ctx, id, agent); err != nil {
		return err
	}
	r.mu.Lock()
	agent.ID = id
	r.agents[id] = agent
	r.mu.Unlock()
	return nil
}

func (r *Registry) Delete(ctx context.Context, id string) error {
	if err := r.store.DeleteAgent(ctx, id); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.agents, id)
	r.mu.Unlock()
	return nil
}

// SetStatus updates an agent's status in both cache and DB.
func (r *Registry) SetStatus(ctx context.Context, id, status string) {
	r.mu.Lock()
	if a, ok := r.agents[id]; ok {
		a.Status = status
		r.agents[id] = a
	}
	r.mu.Unlock()

	if err := r.store.UpdateAgentStatus(ctx, id, status); err != nil {
		r.logger.Error("failed to update agent status in DB", "agent", id, "error", err)
	}
}

// Discover performs semantic search over registered agents.
func (r *Registry) Discover(ctx context.Context, query string, topN int, minScore float64, includeUnhealthy bool) ([]DiscoverResult, error) {
	if r.embedder == nil {
		return nil, nil
	}

	queryVec, err := r.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}

	// Copy agents under lock, then compute similarities without holding it.
	agents := r.List()

	var results []DiscoverResult
	for _, agent := range agents {
		if !includeUnhealthy && agent.Status == StatusUnhealthy {
			continue
		}
		if len(agent.Embedding) == 0 {
			continue
		}

		score := cosineSimilarity(queryVec, agent.Embedding)
		if score >= minScore {
			results = append(results, DiscoverResult{Agent: agent, Score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if topN > 0 && len(results) > topN {
		results = results[:topN]
	}

	return results, nil
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}
