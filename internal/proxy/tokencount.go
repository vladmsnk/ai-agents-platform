package proxy

import (
	"encoding/json"
)

// Usage represents the token usage from an OpenAI-compatible response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// chatResponse is a partial parse of a non-streaming chat completion response.
type chatResponse struct {
	Usage *Usage `json:"usage"`
}

// parseUsageFromBody extracts token usage from a non-streaming response body.
func parseUsageFromBody(body []byte) Usage {
	var resp chatResponse
	if err := json.Unmarshal(body, &resp); err != nil || resp.Usage == nil {
		return Usage{}
	}
	return *resp.Usage
}

// streamChunkData represents a partial parse of a streaming SSE chunk.
type streamChunkData struct {
	Usage *Usage `json:"usage"`
}

// ComputeCost calculates the total cost based on token usage and per-token prices.
func (u Usage) ComputeCost(pricePerInput, pricePerOutput float64) float64 {
	return float64(u.PromptTokens)*pricePerInput + float64(u.CompletionTokens)*pricePerOutput
}

// parseUsageFromSSEChunk tries to extract usage from a streaming SSE data line.
func parseUsageFromSSEChunk(data []byte) Usage {
	var chunk streamChunkData
	if err := json.Unmarshal(data, &chunk); err != nil || chunk.Usage == nil {
		return Usage{}
	}
	return *chunk.Usage
}
