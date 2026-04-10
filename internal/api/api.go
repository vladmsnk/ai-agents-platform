package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/vymoiseenkov/ai-agents-platform/internal/a2a"
	"github.com/vymoiseenkov/ai-agents-platform/internal/balancer"
	"github.com/vymoiseenkov/ai-agents-platform/internal/config"
	"github.com/vymoiseenkov/ai-agents-platform/internal/health"
	"github.com/vymoiseenkov/ai-agents-platform/internal/stats"
	"github.com/vymoiseenkov/ai-agents-platform/internal/storage"
)

type API struct {
	store    *storage.Store
	balancer *balancer.Balancer
	health   *health.Checker
	stats    *stats.Collector
	registry *a2a.Registry
	logger   *slog.Logger
}

func New(store *storage.Store, b *balancer.Balancer, h *health.Checker, s *stats.Collector, registry *a2a.Registry, logger *slog.Logger) *API {
	return &API{store: store, balancer: b, health: h, stats: s, registry: registry, logger: logger}
}

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/providers", a.handleProviders)
	mux.HandleFunc("/api/providers/", a.handleProvider)
	mux.HandleFunc("/api/stats", a.handleStats)
	mux.HandleFunc("/api/health", a.handleHealthAll)
	mux.HandleFunc("/api/health/", a.handleHealthOne)
	mux.HandleFunc("/api/agents", a.handleAgents)
	mux.HandleFunc("/api/agents/", a.handleAgent)
}

func (a *API) handleProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listProviders(w, r)
	case http.MethodPost:
		a.addProvider(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) handleProvider(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/providers/")
	if name == "" {
		http.Error(w, "provider name required", http.StatusBadRequest)
		return
	}

	if strings.HasSuffix(name, "/test") {
		name = strings.TrimSuffix(name, "/test")
		if r.Method == http.MethodPost {
			a.testProvider(w, r, name)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.getProvider(w, r, name)
	case http.MethodPut:
		a.updateProvider(w, r, name)
	case http.MethodDelete:
		a.deleteProvider(w, r, name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type providerResponse struct {
	config.Provider
	Health *health.Status `json:"health,omitempty"`
}

func (a *API) listProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := a.store.ListProviders(r.Context())
	if err != nil {
		a.logger.Error("failed to list providers", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	healthMap := a.health.All()
	resp := make([]providerResponse, len(providers))
	for i, p := range providers {
		resp[i] = providerResponse{Provider: p}
		if s, ok := healthMap[p.Name]; ok {
			resp[i].Health = &s
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) getProvider(w http.ResponseWriter, r *http.Request, name string) {
	p, found, err := a.store.GetProvider(r.Context(), name)
	if err != nil {
		a.logger.Error("failed to get provider", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}

	resp := providerResponse{Provider: p}
	if s, ok := a.health.Get(name); ok {
		resp.Health = &s
	}
	writeJSON(w, http.StatusOK, resp)
}

type providerInput struct {
	Name                string   `json:"name"`
	URL                 string   `json:"url"`
	Models              []string `json:"models"`
	Weight              int      `json:"weight"`
	Enabled             *bool    `json:"enabled"`
	APIKey              string   `json:"api_key"`
	KeyEnv              string   `json:"key_env"`
	Timeout             int      `json:"timeout_seconds"`
	PricePerInputToken  *float64 `json:"price_per_input_token"`
	PricePerOutputToken *float64 `json:"price_per_output_token"`
	RateLimitRPM        *int     `json:"rate_limit_rpm"`
	Priority            *int     `json:"priority"`
}

func (a *API) addProvider(w http.ResponseWriter, r *http.Request) {
	var input providerInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	p := config.Provider{
		Name:    input.Name,
		URL:     input.URL,
		Models:  input.Models,
		Weight:  input.Weight,
		Enabled: true,
		KeyEnv:  input.KeyEnv,
		Timeout: input.Timeout,
	}

	if input.Enabled != nil {
		p.Enabled = *input.Enabled
	}
	if input.APIKey != "" {
		p.APIKey = input.APIKey
	}
	if input.PricePerInputToken != nil {
		p.PricePerInputToken = *input.PricePerInputToken
	}
	if input.PricePerOutputToken != nil {
		p.PricePerOutputToken = *input.PricePerOutputToken
	}
	if input.RateLimitRPM != nil {
		p.RateLimitRPM = *input.RateLimitRPM
	}
	if input.Priority != nil {
		p.Priority = *input.Priority
	}
	config.ApplyDefaults(&p)

	if err := config.ValidateProvider(p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Check uniqueness
	_, found, err := a.store.GetProvider(r.Context(), p.Name)
	if err != nil {
		a.logger.Error("failed to check provider", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if found {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider with this name already exists"})
		return
	}

	if err := a.store.AddProvider(r.Context(), p); err != nil {
		a.logger.Error("failed to add provider", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	a.syncFromDB(r.Context())
	writeJSON(w, http.StatusCreated, p)
}

func (a *API) updateProvider(w http.ResponseWriter, r *http.Request, name string) {
	var input providerInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	existing, found, err := a.store.GetProvider(r.Context(), name)
	if err != nil {
		a.logger.Error("failed to get provider", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}

	// Apply partial updates
	if input.URL != "" {
		existing.URL = input.URL
	}
	if input.Models != nil {
		existing.Models = input.Models
	}
	if input.Weight > 0 {
		existing.Weight = input.Weight
	}
	if input.Enabled != nil {
		existing.Enabled = *input.Enabled
	}
	if input.Timeout > 0 {
		existing.Timeout = input.Timeout
	}
	if input.APIKey != "" {
		existing.APIKey = input.APIKey
	}
	if input.KeyEnv != "" {
		existing.KeyEnv = input.KeyEnv
		existing.APIKey = os.Getenv(input.KeyEnv)
	}
	if input.PricePerInputToken != nil {
		existing.PricePerInputToken = *input.PricePerInputToken
	}
	if input.PricePerOutputToken != nil {
		existing.PricePerOutputToken = *input.PricePerOutputToken
	}
	if input.RateLimitRPM != nil {
		existing.RateLimitRPM = *input.RateLimitRPM
	}
	if input.Priority != nil {
		existing.Priority = *input.Priority
	}

	if err := a.store.UpdateProvider(r.Context(), name, existing); err != nil {
		a.logger.Error("failed to update provider", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	a.syncFromDB(r.Context())
	writeJSON(w, http.StatusOK, existing)
}

func (a *API) deleteProvider(w http.ResponseWriter, r *http.Request, name string) {
	// Find orphaned models before deleting
	providers, _ := a.store.ListProviders(r.Context())
	var deletedModels []string
	for _, p := range providers {
		if p.Name == name {
			deletedModels = p.Models
			break
		}
	}

	orphanedModels := []string{}
	for _, m := range deletedModels {
		servedByOthers := false
		for _, p := range providers {
			if p.Name == name {
				continue
			}
			for _, pm := range p.Models {
				if pm == m {
					servedByOthers = true
					break
				}
			}
			if servedByOthers {
				break
			}
		}
		if !servedByOthers {
			orphanedModels = append(orphanedModels, m)
		}
	}

	if err := a.store.DeleteProvider(r.Context(), name); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "provider not found", http.StatusNotFound)
			return
		}
		a.logger.Error("failed to delete provider", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	a.syncFromDB(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":         name,
		"orphaned_models": orphanedModels,
	})
}

func (a *API) testProvider(w http.ResponseWriter, r *http.Request, name string) {
	p, found, _ := a.store.GetProvider(r.Context(), name)
	var url string
	if found {
		url = p.URL
	}
	if url == "" {
		var input struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.URL == "" {
			http.Error(w, "provider not found and no URL provided", http.StatusNotFound)
			return
		}
		url = input.URL
	}

	status := a.health.CheckOne(name, url)
	writeJSON(w, http.StatusOK, status)
}

func (a *API) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snap := a.stats.Snapshot()
	writeJSON(w, http.StatusOK, snap)
}

func (a *API) handleHealthAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, a.health.All())
}

func (a *API) handleHealthOne(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/health/")
	if name == "" {
		http.Error(w, "provider name required", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s, ok := a.health.Get(name)
	if !ok {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// syncFromDB reloads providers from the database and updates the balancer and health checker.
func (a *API) syncFromDB(ctx context.Context) {
	providers, err := a.store.ListProviders(ctx)
	if err != nil {
		a.logger.Error("failed to sync providers from db", "error", err)
		return
	}

	a.balancer.UpdateProviders(providers)
	a.health.SetProviders(config.ProviderURLs(providers))
}

// --- Agent CRUD ---

func (a *API) handleAgents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.registry.List())
	case http.MethodPost:
		var agent a2a.AgentCard
		if err := json.NewDecoder(r.Body).Decode(&agent); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if agent.ID == "" || agent.Name == "" || agent.URL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id, name, and url are required"})
			return
		}
		if err := a.registry.Add(r.Context(), agent); err != nil {
			a.logger.Error("failed to add agent", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, agent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) handleAgent(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	if id == "" {
		http.Error(w, "agent id required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		agent, ok := a.registry.Get(id)
		if !ok {
			http.Error(w, "agent not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, agent)
	case http.MethodPut:
		var agent a2a.AgentCard
		if err := json.NewDecoder(r.Body).Decode(&agent); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if err := a.registry.Update(r.Context(), id, agent); err != nil {
			a.logger.Error("failed to update agent", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		agent.ID = id
		writeJSON(w, http.StatusOK, agent)
	case http.MethodDelete:
		if err := a.registry.Delete(r.Context(), id); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, "agent not found", http.StatusNotFound)
				return
			}
			a.logger.Error("failed to delete agent", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
