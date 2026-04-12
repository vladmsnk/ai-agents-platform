package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenClient obtains and caches OAuth2 client_credentials tokens from Keycloak.
// Used by the gateway to authenticate itself when dispatching to downstream agents.
type TokenClient struct {
	tokenURL     string
	clientID     string
	clientSecret string

	mu      sync.Mutex
	token   string
	expiry  time.Time
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// NewTokenClient creates a client for the OAuth2 client_credentials flow.
func NewTokenClient(keycloakURL, realm, clientID, clientSecret string) *TokenClient {
	base := strings.TrimRight(keycloakURL, "/")
	return &TokenClient{
		tokenURL:     base + "/realms/" + realm + "/protocol/openid-connect/token",
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

// GetToken returns a valid access token, refreshing if necessary.
// The token is cached until 30 seconds before expiry to avoid edge-case rejections.
func (tc *TokenClient) GetToken(ctx context.Context) (string, error) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.token != "" && time.Now().Before(tc.expiry) {
		return tc.token, nil
	}

	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {tc.clientID},
		"client_secret": {tc.clientSecret},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tc.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request to %s: %w", tc.tokenURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}

	var tokenResp tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	tc.token = tokenResp.AccessToken
	// Expire 30 seconds early to avoid using a near-expired token.
	tc.expiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn)*time.Second - 30*time.Second)

	return tc.token, nil
}
