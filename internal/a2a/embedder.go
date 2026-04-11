package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Embedder calls the gateway's /v1/embeddings endpoint to vectorize text.
type Embedder struct {
	gatewayURL string
	model      string
	client     *http.Client
}

func NewEmbedder(gatewayURL, model string) *Embedder {
	return &Embedder{
		gatewayURL: strings.TrimRight(gatewayURL, "/"),
		model:      model,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type embeddingReq struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embeddingResp struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// Embed sends text to the gateway's embeddings endpoint and returns the vector.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float64, error) {
	body, err := json.Marshal(embeddingReq{Model: e.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.gatewayURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding request returned status %d", resp.StatusCode)
	}

	var result embeddingResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}

	if len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("empty embedding in response")
	}

	return result.Data[0].Embedding, nil
}

// BuildEmbeddingText constructs the text to embed from an agent's description and skills.
func BuildEmbeddingText(agent AgentCard) string {
	var sb strings.Builder
	sb.WriteString(agent.Description)

	if len(agent.Skills) > 0 {
		sb.WriteString(". Skills: ")
		for i, s := range agent.Skills {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(s.Name)
			if s.Description != "" {
				sb.WriteString(" (")
				sb.WriteString(s.Description)
				sb.WriteString(")")
			}
		}
	}

	return sb.String()
}
