package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

type contextKey string

const ClaimsKey contextKey = "auth_claims"

// Claims represents the JWT claims we care about from Keycloak.
type Claims struct {
	Subject       string         `json:"sub"`
	Issuer        string         `json:"iss"`
	Audience      jwt.Audience   `json:"aud"`
	ExpiresAt     *jwt.NumericDate `json:"exp"`
	IssuedAt      *jwt.NumericDate `json:"iat"`
	AuthorizedParty string       `json:"azp"`
	RealmAccess   *RealmAccess   `json:"realm_access,omitempty"`
	Scope         string         `json:"scope,omitempty"`
	ClientID      string         `json:"client_id,omitempty"` // present in client_credentials tokens
}

type RealmAccess struct {
	Roles []string `json:"roles"`
}

// HasRole checks if the claims include a specific realm role.
func (c *Claims) HasRole(role string) bool {
	if c.RealmAccess == nil {
		return false
	}
	for _, r := range c.RealmAccess.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// GetClaims extracts claims from the request context.
func GetClaims(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(ClaimsKey).(*Claims)
	return c, ok
}

// Middleware validates JWT bearer tokens against Keycloak's JWKS endpoint.
type Middleware struct {
	issuerURL string // e.g. http://keycloak:8180/realms/agents
	jwksURL   string
	logger    *slog.Logger

	mu      sync.RWMutex
	jwks    *jose.JSONWebKeySet
	fetched time.Time
}

// NewMiddleware creates JWT validation middleware for a Keycloak realm.
// keycloakURL is used for JWKS fetching (internal network).
// issuerBaseURL is used for token issuer validation (may differ when KC_HOSTNAME differs from internal URL).
// If issuerBaseURL is empty, keycloakURL is used for both.
func NewMiddleware(keycloakURL, issuerBaseURL, realm string, logger *slog.Logger) *Middleware {
	base := strings.TrimRight(keycloakURL, "/")
	issuerBase := base
	if issuerBaseURL != "" {
		issuerBase = strings.TrimRight(issuerBaseURL, "/")
	}
	return &Middleware{
		issuerURL: issuerBase + "/realms/" + realm,
		jwksURL:   base + "/realms/" + realm + "/protocol/openid-connect/certs",
		logger:    logger,
	}
}

// WarmUp pre-fetches the JWKS keys so the first request doesn't block.
func (m *Middleware) WarmUp(ctx context.Context) error {
	_, err := m.getKeys(ctx)
	return err
}

// Protect returns middleware that rejects unauthenticated requests.
func (m *Middleware) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := m.validate(r)
		if err != nil {
			m.logger.Warn("auth: rejected request", "error", err, "path", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized", "message": err.Error()})
			return
		}
		ctx := context.WithValue(r.Context(), ClaimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) validate(r *http.Request) (*Claims, error) {
	token := extractBearer(r)
	if token == "" {
		return nil, fmt.Errorf("missing bearer token")
	}

	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.RS256, jose.ES256})
	if err != nil {
		return nil, fmt.Errorf("invalid token format: %w", err)
	}

	keys, err := m.getKeys(r.Context())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch JWKS: %w", err)
	}

	// Try each key until one works (Keycloak may have multiple active keys).
	var claims Claims
	var validated bool
	for _, key := range keys.Keys {
		if err := parsed.Claims(key, &claims); err == nil {
			validated = true
			break
		}
	}
	if !validated {
		// Keys might be rotated — force refresh and retry once.
		keys, err = m.fetchKeys(r.Context())
		if err != nil {
			return nil, fmt.Errorf("JWKS refresh failed: %w", err)
		}
		for _, key := range keys.Keys {
			if err := parsed.Claims(key, &claims); err == nil {
				validated = true
				break
			}
		}
	}
	if !validated {
		return nil, fmt.Errorf("token signature verification failed")
	}

	// Validate standard claims.
	expected := jwt.Expected{
		Issuer: m.issuerURL,
		Time:   time.Now(),
	}
	if err := claims.validate(expected); err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}

	return &claims, nil
}

func (c *Claims) validate(expected jwt.Expected) error {
	if c.Issuer != expected.Issuer {
		return fmt.Errorf("unexpected issuer: %s", c.Issuer)
	}
	if c.ExpiresAt != nil && expected.Time.After(c.ExpiresAt.Time()) {
		return fmt.Errorf("token expired")
	}
	return nil
}

// getKeys returns cached JWKS or fetches if stale (>5 min).
func (m *Middleware) getKeys(ctx context.Context) (*jose.JSONWebKeySet, error) {
	m.mu.RLock()
	if m.jwks != nil && time.Since(m.fetched) < 5*time.Minute {
		keys := m.jwks
		m.mu.RUnlock()
		return keys, nil
	}
	m.mu.RUnlock()
	return m.fetchKeys(ctx)
}

func (m *Middleware) fetchKeys(ctx context.Context) (*jose.JSONWebKeySet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock.
	if m.jwks != nil && time.Since(m.fetched) < 5*time.Second {
		return m.jwks, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.jwksURL, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("JWKS fetch from %s: %w", m.jwksURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	var jwks jose.JSONWebKeySet
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}

	m.jwks = &jwks
	m.fetched = time.Now()
	m.logger.Info("auth: refreshed JWKS keys", "count", len(jwks.Keys))
	return m.jwks, nil
}

func extractBearer(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}
