package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// KeycloakAdmin manages OAuth2 clients in Keycloak via the Admin REST API.
type KeycloakAdmin struct {
	adminURL    string // e.g. http://keycloak:8180/admin/realms/agents
	tokenClient *TokenClient
	logger      *slog.Logger
	realmRoles  []string // roles to assign to new service accounts
}

// ClientCredentials holds the OAuth2 credentials for a newly provisioned agent.
type ClientCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// NewKeycloakAdmin creates an admin client. The tokenClient should authenticate
// as a client with the realm-management roles (the "gateway" client works if it
// has manage-clients permissions, or use a dedicated admin-cli client).
func NewKeycloakAdmin(keycloakURL, realm string, tokenClient *TokenClient, logger *slog.Logger) *KeycloakAdmin {
	base := strings.TrimRight(keycloakURL, "/")
	return &KeycloakAdmin{
		adminURL:    base + "/admin/realms/" + realm,
		tokenClient: tokenClient,
		logger:      logger,
		realmRoles:  []string{"a2a-caller"},
	}
}

// ProvisionClient creates a Keycloak OAuth2 client for an agent and assigns realm roles.
// Returns the client_id and generated client_secret.
func (ka *KeycloakAdmin) ProvisionClient(ctx context.Context, agentID, agentName string) (*ClientCredentials, error) {
	token, err := ka.tokenClient.GetToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("get admin token: %w", err)
	}

	clientID := "agent-" + agentID

	// Create the client.
	payload := map[string]any{
		"clientId":                clientID,
		"name":                   agentName,
		"enabled":                true,
		"clientAuthenticatorType": "client-secret",
		"serviceAccountsEnabled": true,
		"directAccessGrantsEnabled": false,
		"publicClient":           false,
		"protocol":               "openid-connect",
		"defaultClientScopes":    []string{"openid"},
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ka.adminURL+"/clients", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create client request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		ka.logger.Info("keycloak: client already exists, fetching secret", "client_id", clientID)
		return ka.getExistingCredentials(ctx, token, clientID)
	}
	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create client: status %d, body: %s", resp.StatusCode, respBody)
	}

	// Extract the internal UUID from the Location header.
	location := resp.Header.Get("Location")
	internalID := location[strings.LastIndex(location, "/")+1:]

	// Fetch the generated secret.
	secret, err := ka.getClientSecret(ctx, token, internalID)
	if err != nil {
		return nil, fmt.Errorf("get client secret: %w", err)
	}

	// Assign realm roles to the service account.
	if err := ka.assignRealmRoles(ctx, token, internalID); err != nil {
		ka.logger.Warn("keycloak: failed to assign roles (client still usable)", "client_id", clientID, "error", err)
	}

	ka.logger.Info("keycloak: provisioned client", "client_id", clientID, "agent", agentName)
	return &ClientCredentials{ClientID: clientID, ClientSecret: secret}, nil
}

// DeleteClient removes a Keycloak client for an agent.
func (ka *KeycloakAdmin) DeleteClient(ctx context.Context, agentID string) error {
	token, err := ka.tokenClient.GetToken(ctx)
	if err != nil {
		return fmt.Errorf("get admin token: %w", err)
	}

	clientID := "agent-" + agentID
	internalID, err := ka.findClientByClientID(ctx, token, clientID)
	if err != nil {
		return err
	}
	if internalID == "" {
		ka.logger.Info("keycloak: client not found, nothing to delete", "client_id", clientID)
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, ka.adminURL+"/clients/"+internalID, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("delete client: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("delete client: unexpected status %d", resp.StatusCode)
	}

	ka.logger.Info("keycloak: deleted client", "client_id", clientID)
	return nil
}

func (ka *KeycloakAdmin) getExistingCredentials(ctx context.Context, token, clientID string) (*ClientCredentials, error) {
	internalID, err := ka.findClientByClientID(ctx, token, clientID)
	if err != nil {
		return nil, err
	}
	if internalID == "" {
		return nil, fmt.Errorf("client %s not found after conflict", clientID)
	}

	secret, err := ka.getClientSecret(ctx, token, internalID)
	if err != nil {
		return nil, err
	}
	return &ClientCredentials{ClientID: clientID, ClientSecret: secret}, nil
}

func (ka *KeycloakAdmin) findClientByClientID(ctx context.Context, token, clientID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ka.adminURL+"/clients?clientId="+clientID, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var clients []struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		return "", err
	}
	if len(clients) == 0 {
		return "", nil
	}
	return clients[0].ID, nil
}

func (ka *KeycloakAdmin) getClientSecret(ctx context.Context, token, internalID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ka.adminURL+"/clients/"+internalID+"/client-secret", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Value, nil
}

func (ka *KeycloakAdmin) assignRealmRoles(ctx context.Context, token, clientInternalID string) error {
	// Get the service account user for this client.
	saReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		ka.adminURL+"/clients/"+clientInternalID+"/service-account-user", nil)
	if err != nil {
		return err
	}
	saReq.Header.Set("Authorization", "Bearer "+token)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	saResp, err := httpClient.Do(saReq)
	if err != nil {
		return fmt.Errorf("get service account user: %w", err)
	}
	defer saResp.Body.Close()

	var saUser struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(saResp.Body).Decode(&saUser); err != nil {
		return err
	}

	// Resolve realm role IDs.
	var rolesToAssign []map[string]string
	for _, roleName := range ka.realmRoles {
		roleReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
			ka.adminURL+"/roles/"+roleName, nil)
		if err != nil {
			continue
		}
		roleReq.Header.Set("Authorization", "Bearer "+token)
		roleResp, err := httpClient.Do(roleReq)
		if err != nil {
			continue
		}
		var role struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		json.NewDecoder(roleResp.Body).Decode(&role)
		roleResp.Body.Close()
		if role.ID != "" {
			rolesToAssign = append(rolesToAssign, map[string]string{"id": role.ID, "name": role.Name})
		}
	}

	if len(rolesToAssign) == 0 {
		return nil
	}

	// Assign roles to the service account user.
	roleBody, _ := json.Marshal(rolesToAssign)
	assignReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		ka.adminURL+"/users/"+saUser.ID+"/role-mappings/realm", bytes.NewReader(roleBody))
	if err != nil {
		return err
	}
	assignReq.Header.Set("Content-Type", "application/json")
	assignReq.Header.Set("Authorization", "Bearer "+token)

	assignResp, err := httpClient.Do(assignReq)
	if err != nil {
		return fmt.Errorf("assign roles: %w", err)
	}
	defer assignResp.Body.Close()

	if assignResp.StatusCode != http.StatusNoContent && assignResp.StatusCode != http.StatusOK {
		return fmt.Errorf("assign roles: unexpected status %d", assignResp.StatusCode)
	}

	return nil
}
