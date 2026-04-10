package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const gatewayURL = "http://localhost:8080"

// ── helpers ──────────────────────────────────────────────────────────────────

func apiGet(t *testing.T, path string) []byte {
	t.Helper()
	resp, err := http.Get(gatewayURL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("GET %s: status %d, body: %s", path, resp.StatusCode, body)
	}
	return body
}

func apiRequest(t *testing.T, method, path string, payload string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, gatewayURL+path, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// ── 1. Health Check ─────────────────────────────────────────────────────────

func TestHealthEndpoint(t *testing.T) {
	body := apiGet(t, "/health")
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["status"] != "ok" {
		t.Fatalf("expected status ok, got %s", result["status"])
	}
}

// ── 2. Provider CRUD ────────────────────────────────────────────────────────

func TestProviderCRUD(t *testing.T) {
	// 2a. List initial providers
	t.Run("ListProviders", func(t *testing.T) {
		body := apiGet(t, "/api/providers")
		var providers []map[string]any
		if err := json.Unmarshal(body, &providers); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if len(providers) < 1 {
			t.Fatal("expected at least 1 provider from config")
		}
		t.Logf("found %d providers", len(providers))
	})

	// 2b. Add a new provider
	t.Run("AddProvider", func(t *testing.T) {
		payload := `{
			"name": "e2e-test-provider",
			"url": "https://openrouter.ai/api",
			"models": ["google/gemma-4-26b-a4b-it:free"],
			"weight": 1,
			"enabled": true,
			"api_key": "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",
			"timeout_seconds": 60
		}`
		resp, body := apiRequest(t, "POST", "/api/providers", payload)
		if resp.StatusCode != 201 {
			t.Fatalf("expected 201, got %d: %s", resp.StatusCode, body)
		}
		t.Logf("added provider: %s", body)
	})

	// 2c. Get the provider
	t.Run("GetProvider", func(t *testing.T) {
		body := apiGet(t, "/api/providers/e2e-test-provider")
		var p map[string]any
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if p["name"] != "e2e-test-provider" {
			t.Fatalf("expected name e2e-test-provider, got %v", p["name"])
		}
	})

	// 2d. Update the provider
	t.Run("UpdateProvider", func(t *testing.T) {
		payload := `{"weight": 3}`
		resp, body := apiRequest(t, "PUT", "/api/providers/e2e-test-provider", payload)
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		var p map[string]any
		json.Unmarshal(body, &p)
		if int(p["weight"].(float64)) != 3 {
			t.Fatalf("expected weight 3, got %v", p["weight"])
		}
	})

	// 2e. Disable the provider
	t.Run("DisableProvider", func(t *testing.T) {
		payload := `{"enabled": false}`
		resp, body := apiRequest(t, "PUT", "/api/providers/e2e-test-provider", payload)
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		var p map[string]any
		json.Unmarshal(body, &p)
		if p["enabled"] != false {
			t.Fatalf("expected enabled=false, got %v", p["enabled"])
		}
	})

	// 2f. Re-enable
	t.Run("EnableProvider", func(t *testing.T) {
		payload := `{"enabled": true}`
		resp, body := apiRequest(t, "PUT", "/api/providers/e2e-test-provider", payload)
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
	})

	// 2g. Duplicate name should fail
	t.Run("DuplicateProvider", func(t *testing.T) {
		payload := `{
			"name": "e2e-test-provider",
			"url": "https://example.com",
			"models": ["test-model"],
			"weight": 1,
			"enabled": true,
			"timeout_seconds": 60
		}`
		resp, _ := apiRequest(t, "POST", "/api/providers", payload)
		if resp.StatusCode != 409 {
			t.Fatalf("expected 409 conflict, got %d", resp.StatusCode)
		}
	})

	// 2h. Delete the provider
	t.Run("DeleteProvider", func(t *testing.T) {
		resp, body := apiRequest(t, "DELETE", "/api/providers/e2e-test-provider", "")
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		var result map[string]any
		json.Unmarshal(body, &result)
		if result["deleted"] != "e2e-test-provider" {
			t.Fatalf("expected deleted=e2e-test-provider, got %v", result["deleted"])
		}
	})

	// 2i. Verify deleted
	t.Run("VerifyDeleted", func(t *testing.T) {
		resp, _ := apiRequest(t, "GET", "/api/providers/e2e-test-provider", "")
		if resp.StatusCode != 404 {
			t.Fatalf("expected 404 after delete, got %d", resp.StatusCode)
		}
	})
}

// ── 3. Provider Health Checks ───────────────────────────────────────────────

func TestProviderHealth(t *testing.T) {
	t.Run("AllHealth", func(t *testing.T) {
		body := apiGet(t, "/api/health")
		var health map[string]any
		if err := json.Unmarshal(body, &health); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		t.Logf("health statuses: %d providers", len(health))
	})

	t.Run("SingleProviderHealth", func(t *testing.T) {
		body := apiGet(t, "/api/health/openrouter-free")
		var status map[string]any
		if err := json.Unmarshal(body, &status); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if _, ok := status["healthy"]; !ok {
			t.Fatal("expected 'healthy' field in response")
		}
		t.Logf("openrouter-free healthy=%v latency=%vms", status["healthy"], status["latency_ms"])
	})

	t.Run("TestConnection", func(t *testing.T) {
		resp, body := apiRequest(t, "POST", "/api/providers/openrouter-free/test", "")
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		var status map[string]any
		json.Unmarshal(body, &status)
		t.Logf("test connection: healthy=%v latency=%vms", status["healthy"], status["latency_ms"])
	})
}

// ── 4. Chat Completions Proxy ───────────────────────────────────────────────

func TestChatCompletions(t *testing.T) {
	client := &http.Client{Timeout: 120 * time.Second}

	models := []struct {
		name     string
		provider string
	}{
		{"google/gemma-4-26b-a4b-it:free", "openrouter-free"},
		{"openai/gpt-4o-mini", "openrouter-openai"},
		{"anthropic/claude-3.5-haiku", "openrouter-anthropic"},
	}

	for _, m := range models {
		t.Run(m.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{
				"model": %q,
				"messages": [{"role": "user", "content": "Say hello in exactly 3 words."}],
				"max_tokens": 20
			}`, m.name)

			// Retry up to 3 times for rate-limited free models
			var lastStatus int
			var lastBody []byte
			for attempt := 0; attempt < 3; attempt++ {
				req, err := http.NewRequest("POST", gatewayURL+"/v1/chat/completions", strings.NewReader(payload))
				if err != nil {
					t.Fatalf("create request: %v", err)
				}
				req.Header.Set("Content-Type", "application/json")

				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("request failed: %v", err)
				}
				lastBody, _ = io.ReadAll(resp.Body)
				resp.Body.Close()
				lastStatus = resp.StatusCode

				if resp.StatusCode == 200 {
					break
				}
				if resp.StatusCode == 429 && attempt < 2 {
					t.Logf("rate limited (attempt %d), retrying in 3s...", attempt+1)
					time.Sleep(3 * time.Second)
					continue
				}
			}

			if lastStatus == 429 {
				t.Skipf("skipping: upstream rate limited after retries (free model): %s", string(lastBody))
			}
			if lastStatus != 200 {
				t.Fatalf("expected 200, got %d: %s", lastStatus, string(lastBody))
			}

			var result map[string]any
			if err := json.Unmarshal(lastBody, &result); err != nil {
				t.Fatalf("invalid JSON response: %v\nbody: %s", err, lastBody)
			}

			choices, ok := result["choices"].([]any)
			if !ok || len(choices) == 0 {
				t.Fatalf("expected choices in response, got: %s", lastBody)
			}

			msg := choices[0].(map[string]any)["message"].(map[string]any)["content"]
			t.Logf("[%s] response: %v", m.provider, msg)
		})
	}
}

// ── 5. Chat Completions — Error Cases ───────────────────────────────────────

func TestChatCompletionsErrors(t *testing.T) {
	t.Run("MissingModel", func(t *testing.T) {
		payload := `{"messages": [{"role": "user", "content": "hi"}]}`
		resp, _ := apiRequest(t, "POST", "/v1/chat/completions", payload)
		if resp.StatusCode != 400 {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("UnknownModel", func(t *testing.T) {
		payload := `{"model": "nonexistent/model-xyz", "messages": [{"role": "user", "content": "hi"}]}`
		resp, _ := apiRequest(t, "POST", "/v1/chat/completions", payload)
		if resp.StatusCode != 404 {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		resp, _ := apiRequest(t, "POST", "/v1/chat/completions", "not json")
		if resp.StatusCode != 400 {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("WrongMethod", func(t *testing.T) {
		resp, _ := apiRequest(t, "GET", "/v1/chat/completions", "")
		if resp.StatusCode != 405 {
			t.Fatalf("expected 405, got %d", resp.StatusCode)
		}
	})
}

// ── 6. Stats Endpoint ───────────────────────────────────────────────────────

func TestStats(t *testing.T) {
	body := apiGet(t, "/api/stats")
	var stats map[string]any
	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// After the chat completion tests, we should have some data
	totalReqs := int(stats["total_requests"].(float64))
	t.Logf("total_requests=%d active_providers=%v avg_latency=%vms error_rate=%v%%",
		totalReqs, stats["active_providers"], stats["avg_latency_ms"], stats["error_rate"])

	if totalReqs < 1 {
		t.Fatal("expected at least 1 request recorded in stats")
	}

	// Check by_provider exists
	byProvider, ok := stats["by_provider"].([]any)
	if !ok {
		t.Fatal("expected by_provider array")
	}
	if len(byProvider) < 1 {
		t.Fatal("expected at least 1 provider in stats")
	}

	for _, p := range byProvider {
		ps := p.(map[string]any)
		t.Logf("  provider=%s requests=%v rpm=%.1f p50=%vms p95=%vms errors=%v%%",
			ps["name"], ps["total_requests"], ps["rpm"], ps["latency_p50_ms"], ps["latency_p95_ms"], ps["error_rate"])
	}

	// Check by_model exists
	byModel, ok := stats["by_model"].([]any)
	if !ok {
		t.Fatal("expected by_model array")
	}
	if len(byModel) < 1 {
		t.Fatal("expected at least 1 model in stats")
	}
}

// ── 7. Prometheus Metrics ───────────────────────────────────────────────────

func TestPrometheusMetrics(t *testing.T) {
	body := apiGet(t, "/metrics")
	metrics := string(body)

	required := []string{
		"gateway_requests_total",
		"gateway_request_duration",
		"gateway_requests_active",
		"gateway_goroutines",
		"gateway_cpu_usage",
	}

	for _, name := range required {
		if !strings.Contains(metrics, name) {
			t.Errorf("expected metric %q in /metrics output", name)
		}
	}

	// Verify request metrics have provider/model labels from our chat completions
	if !strings.Contains(metrics, `provider="openrouter-`) {
		t.Error("expected provider label in metrics")
	}

	t.Logf("metrics output length: %d bytes", len(metrics))
}

// ── 8. Streaming ────────────────────────────────────────────────────────────

func TestStreamingCompletion(t *testing.T) {
	payload := `{
		"model": "openai/gpt-4o-mini",
		"messages": [{"role": "user", "content": "Count from 1 to 3."}],
		"stream": true,
		"max_tokens": 30
	}`

	req, err := http.NewRequest("POST", gatewayURL+"/v1/chat/completions", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	// Read streaming SSE chunks
	body, _ := io.ReadAll(resp.Body)
	content := string(body)

	if !strings.Contains(content, "data: ") {
		t.Fatalf("expected SSE data chunks, got: %s", content[:min(200, len(content))])
	}

	chunks := strings.Count(content, "data: ")
	t.Logf("received %d SSE chunks, total %d bytes", chunks, len(content))
}

// ── 9. Weighted Load Balancing ──────────────────────────────────────────────

func TestLoadBalancing(t *testing.T) {
	// Add two providers for the same model with different weights
	p1 := `{
		"name": "lb-test-a",
		"url": "https://openrouter.ai/api",
		"models": ["google/gemma-4-26b-a4b-it:free"],
		"weight": 8,
		"enabled": true,
		"api_key": "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",
		"timeout_seconds": 120
	}`
	p2 := `{
		"name": "lb-test-b",
		"url": "https://openrouter.ai/api",
		"models": ["google/gemma-4-26b-a4b-it:free"],
		"weight": 2,
		"enabled": true,
		"api_key": "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",
		"timeout_seconds": 120
	}`

	resp1, body1 := apiRequest(t, "POST", "/api/providers", p1)
	if resp1.StatusCode != 201 {
		t.Fatalf("failed to add lb-test-a: %d %s", resp1.StatusCode, body1)
	}
	resp2, body2 := apiRequest(t, "POST", "/api/providers", p2)
	if resp2.StatusCode != 201 {
		t.Fatalf("failed to add lb-test-b: %d %s", resp2.StatusCode, body2)
	}

	// Verify both appear in the provider list
	body := apiGet(t, "/api/providers")
	var providers []map[string]any
	json.Unmarshal(body, &providers)

	foundA, foundB := false, false
	for _, p := range providers {
		if p["name"] == "lb-test-a" {
			foundA = true
		}
		if p["name"] == "lb-test-b" {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Fatal("expected both lb-test-a and lb-test-b to be registered")
	}

	t.Logf("load balancing: lb-test-a (weight=8) + lb-test-b (weight=2) registered for gemma model")

	// Clean up
	apiRequest(t, "DELETE", "/api/providers/lb-test-a", "")
	apiRequest(t, "DELETE", "/api/providers/lb-test-b", "")
}

// ── 10. Validation ──────────────────────────────────────────────────────────

func TestProviderValidation(t *testing.T) {
	t.Run("MissingName", func(t *testing.T) {
		payload := `{"url": "https://example.com", "models": ["m1"]}`
		resp, _ := apiRequest(t, "POST", "/api/providers", payload)
		if resp.StatusCode != 400 {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("MissingURL", func(t *testing.T) {
		payload := `{"name": "test-val", "models": ["m1"]}`
		resp, _ := apiRequest(t, "POST", "/api/providers", payload)
		if resp.StatusCode != 400 {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("MissingModels", func(t *testing.T) {
		payload := `{"name": "test-val", "url": "https://example.com"}`
		resp, _ := apiRequest(t, "POST", "/api/providers", payload)
		if resp.StatusCode != 400 {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})
}
