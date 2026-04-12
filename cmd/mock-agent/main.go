package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type agentCard struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	URL          string       `json:"url"`
	Version      string       `json:"version"`
	Capabilities capabilities `json:"capabilities"`
	Skills       []skill      `json:"skills"`
}

type capabilities struct {
	Streaming              bool `json:"streaming"`
	PushNotifications      bool `json:"pushNotifications"`
	StateTransitionHistory bool `json:"stateTransitionHistory"`
}

type skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
}

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	ID      any             `json:"id"`
	Params  json.RawMessage `json:"params"`
}

type jsonrpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type messageSendParams struct {
	ID      string `json:"id"`
	Message struct {
		Role  string `json:"role"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"message"`
}

type discoverResult struct {
	Agent    agentCard `json:"agent"`
	Score    float64   `json:"score"`
	ProxyURL string    `json:"proxy_url"`
}

// tokenClient fetches and caches OAuth2 client_credentials tokens from Keycloak.
type tokenClient struct {
	tokenURL     string
	clientID     string
	clientSecret string

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func newTokenClient(keycloakURL, realm, clientID, clientSecret string) *tokenClient {
	base := strings.TrimRight(keycloakURL, "/")
	return &tokenClient{
		tokenURL:     base + "/realms/" + realm + "/protocol/openid-connect/token",
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

func (tc *tokenClient) getToken() (string, error) {
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

	resp, err := http.Post(tc.tokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}

	tc.token = tokenResp.AccessToken
	tc.expiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn)*time.Second - 30*time.Second)
	return tc.token, nil
}

// authClient wraps an http.Client to automatically attach bearer tokens.
type authClient struct {
	inner *http.Client
	tc    *tokenClient // nil when auth is disabled
}

func (ac *authClient) do(req *http.Request) (*http.Response, error) {
	if ac.tc != nil {
		token, err := ac.tc.getToken()
		if err != nil {
			return nil, fmt.Errorf("get auth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return ac.inner.Do(req)
}

func (ac *authClient) post(url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return ac.do(req)
}

// agentConfig holds per-agent settings loaded from env vars.
type agentConfig struct {
	name        string
	description string
	agentURL    string
	gatewayURL  string
	listen      string
	skills      []skill
	// Delegation: if task text contains delegateKeyword, discover an agent
	// matching delegateQuery and send it part of the work via the proxy.
	delegateKeyword string // e.g. "translate"
	delegateQuery   string // e.g. "translate text to another language"
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg := agentConfig{
		name:            envOrDefault("AGENT_NAME", "mock-agent"),
		description:     envOrDefault("AGENT_DESCRIPTION", "A mock agent for testing"),
		agentURL:        envOrDefault("AGENT_URL", "http://localhost:9001"),
		gatewayURL:      envOrDefault("GATEWAY_URL", "http://localhost:8080"),
		listen:          envOrDefault("LISTEN", ":9001"),
		delegateKeyword: os.Getenv("DELEGATE_KEYWORD"), // optional
		delegateQuery:   os.Getenv("DELEGATE_QUERY"),   // optional
	}
	registryURL := envOrDefault("REGISTRY_URL", "http://localhost:8080")

	// Parse skills from comma-separated env
	for _, s := range strings.Split(envOrDefault("AGENT_SKILLS", "test"), ",") {
		s = strings.TrimSpace(s)
		cfg.skills = append(cfg.skills, skill{
			ID:          strings.ReplaceAll(strings.ToLower(s), " ", "-"),
			Name:        s,
			Description: s,
		})
	}

	card := agentCard{
		ID:          strings.ReplaceAll(strings.ToLower(cfg.name), " ", "-"),
		Name:        cfg.name,
		Description: cfg.description,
		URL:         cfg.agentURL,
		Version:     "1.0.0",
		Skills:      cfg.skills,
	}

	// Set up authenticated HTTP client if Keycloak is configured.
	var tc *tokenClient
	if keycloakURL := os.Getenv("KEYCLOAK_URL"); keycloakURL != "" {
		kcRealm := envOrDefault("KEYCLOAK_REALM", "agents")
		kcClientID := os.Getenv("KEYCLOAK_CLIENT_ID")
		kcClientSecret := os.Getenv("KEYCLOAK_CLIENT_SECRET")
		if kcClientID != "" && kcClientSecret != "" {
			tc = newTokenClient(keycloakURL, kcRealm, kcClientID, kcClientSecret)
			logger.Info("keycloak auth enabled for agent", "client_id", kcClientID)
		}
	}

	client := &authClient{
		inner: &http.Client{Timeout: 60 * time.Second},
		tc:    tc,
	}

	// Start HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/a2a", handleA2A(logger, client, cfg))

	go func() {
		logger.Info("starting mock agent", "name", cfg.name, "listen", cfg.listen)
		if err := http.ListenAndServe(cfg.listen, mux); err != nil {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Register with the registry (retry until gateway is ready)
	registerWithRetry(logger, registryURL, card, client)

	select {}
}

func registerWithRetry(logger *slog.Logger, registryURL string, card agentCard, client *authClient) {
	body, _ := json.Marshal(card)

	for attempt := 1; attempt <= 30; attempt++ {
		resp, err := client.post(registryURL+"/api/agents", "application/json", bytes.NewReader(body))
		if err != nil {
			logger.Warn("registration attempt failed, retrying...", "attempt", attempt, "error", err)
			time.Sleep(2 * time.Second)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusConflict {
			logger.Info("registered with registry", "name", card.Name, "status", resp.StatusCode)
			return
		}

		logger.Warn("registration returned unexpected status", "status", resp.StatusCode, "attempt", attempt)
		time.Sleep(2 * time.Second)
	}

	logger.Error("failed to register after all retries", "name", card.Name)
}

func handleA2A(logger *slog.Logger, client *authClient, cfg agentConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req jsonrpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONRPCError(w, nil, -32700, "parse error")
			return
		}

		switch req.Method {
		case "message/send", "tasks/send":
			handleMessageSend(w, req, client, cfg, logger)
		case "message/stream", "tasks/sendSubscribe":
			handleMessageStream(w, req, client, cfg, logger)
		default:
			writeJSONRPCError(w, req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func handleMessageSend(w http.ResponseWriter, req jsonrpcRequest, client *authClient, cfg agentConfig, logger *slog.Logger) {
	var params messageSendParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, -32602, "invalid params")
		return
	}

	userText := extractText(params)

	logger.Info("received task", "agent", cfg.name, "task_id", params.ID, "text", userText)

	// Step 1: Do our own work via LLM
	ownResponse := callLLM(client, cfg.gatewayURL, cfg.name, userText, logger)
	logger.Info("own work done", "agent", cfg.name, "response_len", len(ownResponse))

	// Step 2: Check if we should delegate part of the work
	var delegateResponse string
	if cfg.delegateKeyword != "" && strings.Contains(strings.ToLower(userText), strings.ToLower(cfg.delegateKeyword)) {
		logger.Info("delegation triggered", "agent", cfg.name, "keyword", cfg.delegateKeyword)
		delegateResponse = delegateWork(client, cfg, ownResponse, logger)
	}

	// Build final response
	var responseText string
	if delegateResponse != "" {
		responseText = fmt.Sprintf("=== %s result ===\n%s\n\n=== Delegated result ===\n%s", cfg.name, ownResponse, delegateResponse)
	} else {
		responseText = ownResponse
	}

	writeTaskResult(w, req.ID, params.ID, "completed", responseText)
}

func handleMessageStream(w http.ResponseWriter, req jsonrpcRequest, client *authClient, cfg agentConfig, logger *slog.Logger) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONRPCError(w, req.ID, -32000, "streaming not supported")
		return
	}

	var params messageSendParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, -32602, "invalid params")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Working
	sendSSE(w, flusher, params.ID, "working", "")

	userText := extractText(params)
	ownResponse := callLLM(client, cfg.gatewayURL, cfg.name, userText, logger)

	var delegateResponse string
	if cfg.delegateKeyword != "" && strings.Contains(strings.ToLower(userText), strings.ToLower(cfg.delegateKeyword)) {
		delegateResponse = delegateWork(client, cfg, ownResponse, logger)
	}

	var responseText string
	if delegateResponse != "" {
		responseText = fmt.Sprintf("=== %s result ===\n%s\n\n=== Delegated result ===\n%s", cfg.name, ownResponse, delegateResponse)
	} else {
		responseText = ownResponse
	}

	// Completed
	sendSSE(w, flusher, params.ID, "completed", responseText)
}

// delegateWork discovers a suitable agent and sends work through the gateway proxy.
func delegateWork(client *authClient, cfg agentConfig, textToDelegate string, logger *slog.Logger) string {
	// Step 1: Discover an agent
	logger.Info("discovering agent", "query", cfg.delegateQuery)

	discoverBody, _ := json.Marshal(map[string]any{
		"query": cfg.delegateQuery,
		"top_n": 1,
	})

	resp, err := client.post(cfg.gatewayURL+"/api/agents/discover", "application/json", bytes.NewReader(discoverBody))
	if err != nil {
		logger.Error("discover call failed", "error", err)
		return "[delegation failed: discover error]"
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		logger.Error("discover returned error", "status", resp.StatusCode, "body", string(body))
		return "[delegation failed: discover returned " + fmt.Sprint(resp.StatusCode) + "]"
	}

	var results []discoverResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil || len(results) == 0 {
		logger.Warn("no agents found for delegation", "query", cfg.delegateQuery)
		return "[delegation failed: no suitable agent found]"
	}

	target := results[0]
	logger.Info("discovered agent for delegation",
		"target_agent", target.Agent.Name,
		"score", fmt.Sprintf("%.3f", target.Score),
		"proxy_url", target.ProxyURL,
	)

	// Step 2: Call the agent through the gateway proxy
	a2aBody, _ := json.Marshal(jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "message/send",
		ID:      1,
		Params: mustMarshalRaw(messageSendParams{
			ID: fmt.Sprintf("delegated-%d", time.Now().UnixMilli()),
			Message: struct {
				Role  string `json:"role"`
				Parts []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"parts"`
			}{
				Role: "user",
				Parts: []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}{
					{Type: "text", Text: textToDelegate},
				},
			},
		}),
	})

	proxyURL := target.ProxyURL
	logger.Info("sending delegated task via proxy", "url", proxyURL)

	proxyResp, err := client.post(proxyURL, "application/json", bytes.NewReader(a2aBody))
	if err != nil {
		logger.Error("proxy call failed", "error", err)
		return "[delegation failed: proxy error]"
	}
	defer proxyResp.Body.Close()

	var rpcResp jsonrpcResponse
	if err := json.NewDecoder(proxyResp.Body).Decode(&rpcResp); err != nil {
		logger.Error("failed to decode proxy response", "error", err)
		return "[delegation failed: decode error]"
	}

	if rpcResp.Error != nil {
		errBytes, _ := json.Marshal(rpcResp.Error)
		logger.Error("proxy returned error", "error", string(errBytes))
		return "[delegation failed: agent error]"
	}

	// Extract text from the task result
	taskBytes, _ := json.Marshal(rpcResp.Result)
	var task struct {
		Artifacts []struct {
			Parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(taskBytes, &task); err == nil {
		for _, a := range task.Artifacts {
			for _, p := range a.Parts {
				if p.Type == "text" {
					logger.Info("delegation completed", "target_agent", target.Agent.Name, "response_len", len(p.Text))
					return p.Text
				}
			}
		}
	}

	return "[delegation completed but no text in response]"
}

// --- Helpers ---

func extractText(params messageSendParams) string {
	for _, p := range params.Message.Parts {
		if p.Type == "text" {
			return p.Text
		}
	}
	return ""
}

func callLLM(client *authClient, gatewayURL, agentName, userText string, logger *slog.Logger) string {
	prompt := fmt.Sprintf("You are %s. Respond briefly to: %s", agentName, userText)
	body, _ := json.Marshal(map[string]any{
		"model": envOrDefault("LLM_MODEL", "google/gemma-4-26b-a4b-it:free"),
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 200,
	})

	resp, err := client.post(gatewayURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		logger.Warn("LLM call failed, returning mock response", "error", err)
		return fmt.Sprintf("[%s mock response] Processed: %s", agentName, userText)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Warn("LLM returned non-200", "status", resp.StatusCode)
		return fmt.Sprintf("[%s mock response] Processed: %s", agentName, userText)
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil || len(chatResp.Choices) == 0 {
		return fmt.Sprintf("[%s mock response] Processed: %s", agentName, userText)
	}

	return chatResp.Choices[0].Message.Content
}

func writeTaskResult(w http.ResponseWriter, rpcID any, taskID, state, text string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      rpcID,
		Result: map[string]any{
			"id":     taskID,
			"status": map[string]any{"state": state},
			"artifacts": []map[string]any{
				{
					"name":  "response",
					"parts": []map[string]any{{"type": "text", "text": text}},
				},
			},
		},
	})
}

func writeJSONRPCError(w http.ResponseWriter, id any, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   map[string]any{"code": code, "message": message},
	})
}

func sendSSE(w http.ResponseWriter, flusher http.Flusher, taskID, state, text string) {
	result := map[string]any{
		"id":     taskID,
		"status": map[string]any{"state": state},
	}
	if text != "" {
		result["artifacts"] = []map[string]any{
			{
				"name":  "response",
				"parts": []map[string]any{{"type": "text", "text": text}},
			},
		}
	}
	data, _ := json.Marshal(result)
	fmt.Fprintf(w, "event: status\ndata: %s\n\n", data)
	flusher.Flush()
}

func mustMarshalRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
