package a2a

import (
	"context"
	"sync"
)

// Store defines the persistence interface for agents.
type Store interface {
	ListAgents(ctx context.Context) ([]AgentCard, error)
	GetAgent(ctx context.Context, id string) (AgentCard, bool, error)
	AddAgent(ctx context.Context, agent AgentCard) error
	UpdateAgent(ctx context.Context, id string, agent AgentCard) error
	DeleteAgent(ctx context.Context, id string) error
}

// Registry manages agent cards with in-memory cache backed by persistent storage.
type Registry struct {
	mu     sync.RWMutex
	agents map[string]AgentCard
	store  Store
}

func NewRegistry(store Store) *Registry {
	return &Registry{
		agents: make(map[string]AgentCard),
		store:  store,
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
	if err := r.store.AddAgent(ctx, agent); err != nil {
		return err
	}
	r.mu.Lock()
	r.agents[agent.ID] = agent
	r.mu.Unlock()
	return nil
}

func (r *Registry) Update(ctx context.Context, id string, agent AgentCard) error {
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
