package mlflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// Client is an HTTP client for the MLflow Tracking API.
type Client struct {
	baseURL      string
	httpClient   *http.Client
	logger       *slog.Logger
	experimentID string
}

// New creates a new MLflow client. Returns nil if trackingURL is empty (disabled).
func New(trackingURL string, logger *slog.Logger) *Client {
	if trackingURL == "" {
		return nil
	}
	return &Client{
		baseURL: trackingURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		logger: logger,
	}
}

// EnsureExperiment creates or gets the experiment for the gateway.
func (c *Client) EnsureExperiment(name string) error {
	if c == nil {
		return nil
	}

	// Try to create
	body, _ := json.Marshal(map[string]string{"name": name})
	resp, err := c.httpClient.Post(c.baseURL+"/api/2.0/mlflow/experiments/create", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create experiment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var result struct {
			ExperimentID string `json:"experiment_id"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		c.experimentID = result.ExperimentID
		return nil
	}

	// Already exists — get by name
	resp2, err := c.httpClient.Get(fmt.Sprintf("%s/api/2.0/mlflow/experiments/get-by-name?experiment_name=%s", c.baseURL, url.QueryEscape(name)))
	if err != nil {
		return fmt.Errorf("get experiment: %w", err)
	}
	defer resp2.Body.Close()

	var result struct {
		Experiment struct {
			ExperimentID string `json:"experiment_id"`
		} `json:"experiment"`
	}
	json.NewDecoder(resp2.Body).Decode(&result)
	c.experimentID = result.Experiment.ExperimentID
	return nil
}

// RunParams holds the parameters for an MLflow run.
type RunParams struct {
	Provider     string
	Model        string
	Stream       bool
	LatencyMs    float64
	TTFTMs       float64
	TPOTMs       float64
	InputTokens  int
	OutputTokens int
	Cost         float64
	StatusCode   int
	IsError      bool
}

// LogRun logs a single LLM request as an MLflow run.
func (c *Client) LogRun(params RunParams) {
	if c == nil {
		return
	}

	// Create run
	runReq := map[string]any{
		"experiment_id": c.experimentID,
		"start_time":    time.Now().UnixMilli(),
		"tags": []map[string]string{
			{"key": "provider", "value": params.Provider},
			{"key": "model", "value": params.Model},
			{"key": "stream", "value": fmt.Sprintf("%v", params.Stream)},
		},
	}
	body, _ := json.Marshal(runReq)
	resp, err := c.httpClient.Post(c.baseURL+"/api/2.0/mlflow/runs/create", "application/json", bytes.NewReader(body))
	if err != nil {
		c.logger.Debug("mlflow: failed to create run", "error", err)
		return
	}
	defer resp.Body.Close()

	var runResult struct {
		Run struct {
			Info struct {
				RunID string `json:"run_id"`
			} `json:"info"`
		} `json:"run"`
	}
	json.NewDecoder(resp.Body).Decode(&runResult)
	runID := runResult.Run.Info.RunID
	if runID == "" {
		return
	}

	// Log metrics
	metrics := []map[string]any{
		{"key": "latency_ms", "value": params.LatencyMs, "timestamp": time.Now().UnixMilli(), "step": 0},
		{"key": "ttft_ms", "value": params.TTFTMs, "timestamp": time.Now().UnixMilli(), "step": 0},
		{"key": "tpot_ms", "value": params.TPOTMs, "timestamp": time.Now().UnixMilli(), "step": 0},
		{"key": "input_tokens", "value": float64(params.InputTokens), "timestamp": time.Now().UnixMilli(), "step": 0},
		{"key": "output_tokens", "value": float64(params.OutputTokens), "timestamp": time.Now().UnixMilli(), "step": 0},
		{"key": "cost_usd", "value": params.Cost, "timestamp": time.Now().UnixMilli(), "step": 0},
		{"key": "status_code", "value": float64(params.StatusCode), "timestamp": time.Now().UnixMilli(), "step": 0},
	}

	metricsBody, _ := json.Marshal(map[string]any{"run_id": runID, "metrics": metrics})
	resp2, err := c.httpClient.Post(c.baseURL+"/api/2.0/mlflow/runs/log-batch", "application/json", bytes.NewReader(metricsBody))
	if err != nil {
		c.logger.Debug("mlflow: failed to log metrics", "error", err)
		return
	}
	resp2.Body.Close()

	// End run
	status := "FINISHED"
	if params.IsError {
		status = "FAILED"
	}
	endBody, _ := json.Marshal(map[string]any{"run_id": runID, "status": status, "end_time": time.Now().UnixMilli()})
	resp3, err := c.httpClient.Post(c.baseURL+"/api/2.0/mlflow/runs/update", "application/json", bytes.NewReader(endBody))
	if err != nil {
		c.logger.Debug("mlflow: failed to end run", "error", err)
		return
	}
	resp3.Body.Close()
}
