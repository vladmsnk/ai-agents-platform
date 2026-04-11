package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vymoiseenkov/ai-agents-platform/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const taskTTL = 30 * time.Minute

// Handler implements the A2A JSON-RPC endpoint and agent card endpoints.
type Handler struct {
	registry *Registry
	logger   *slog.Logger
	selfCard AgentCard
	client   *http.Client

	mu    sync.RWMutex
	tasks map[string]*taskEntry
}

type taskEntry struct {
	task      *Task
	agentID   string // which agent handled this task
	createdAt time.Time
}

func NewHandler(registry *Registry, logger *slog.Logger, selfCard AgentCard) *Handler {
	h := &Handler{
		registry: registry,
		logger:   logger,
		selfCard: selfCard,
		client:   &http.Client{Timeout: 60 * time.Second},
		tasks:    make(map[string]*taskEntry),
	}
	go h.cleanupLoop()
	return h
}

func (h *Handler) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		h.mu.Lock()
		for id, entry := range h.tasks {
			if time.Since(entry.createdAt) > taskTTL {
				delete(h.tasks, id)
			}
		}
		h.mu.Unlock()
	}
}

// Register adds the A2A routes to the mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/.well-known/agent.json", h.handleSelfCard)
	mux.HandleFunc("/a2a", h.handleJSONRPC)
	mux.HandleFunc("/a2a/", h.handleAgentProxy) // /a2a/{agent-id} — explicit routing
}

func (h *Handler) handleSelfCard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.selfCard)
}

func (h *Handler) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		writeRPCError(w, nil, -32700, "parse error")
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, nil, -32700, "parse error")
		return
	}

	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, -32600, "invalid request: jsonrpc must be 2.0")
		return
	}

	ctx := r.Context()
	switch req.Method {
	// v1.0 methods + v0.3 backward compat
	case "message/send", "tasks/send":
		h.handleMessageSend(ctx, w, req, "")
	case "message/stream", "tasks/sendSubscribe":
		h.handleMessageStream(ctx, w, req, "")
	case "tasks/get":
		h.handleTaskGet(w, req)
	case "tasks/cancel":
		h.handleTaskCancel(w, req)
	case "tasks/list":
		h.handleTaskList(w, req)
	default:
		writeRPCError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// handleAgentProxy handles /a2a/{agent-id} — explicit agent targeting.
func (h *Handler) handleAgentProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentID := strings.TrimPrefix(r.URL.Path, "/a2a/")
	if agentID == "" {
		http.Error(w, "agent id required", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		writeRPCError(w, nil, -32700, "parse error")
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, nil, -32700, "parse error")
		return
	}

	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, -32600, "invalid request: jsonrpc must be 2.0")
		return
	}

	ctx := r.Context()
	switch req.Method {
	case "message/send", "tasks/send":
		h.handleMessageSend(ctx, w, req, agentID)
	case "message/stream", "tasks/sendSubscribe":
		h.handleMessageStream(ctx, w, req, agentID)
	default:
		writeRPCError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// routeToAgent finds the best agent — either by explicit ID or semantic discovery.
func (h *Handler) routeToAgent(ctx context.Context, targetAgentID string, params MessageSendParams) (AgentCard, float64, error) {
	// Explicit targeting
	if targetAgentID != "" {
		agent, ok := h.registry.Get(targetAgentID)
		if !ok {
			return AgentCard{}, 0, fmt.Errorf("agent %q not found", targetAgentID)
		}
		return agent, 1.0, nil
	}

	// Semantic auto-routing: extract text from message parts
	var queryParts []string
	for _, p := range params.Message.Parts {
		if p.Type == "text" && p.Text != "" {
			queryParts = append(queryParts, p.Text)
		}
	}
	query := strings.Join(queryParts, " ")
	if query == "" {
		return AgentCard{}, 0, fmt.Errorf("empty message, cannot route")
	}

	results, err := h.registry.Discover(ctx, query, 1, 0.1, false)
	if err != nil {
		return AgentCard{}, 0, fmt.Errorf("discover failed: %w", err)
	}
	if len(results) == 0 {
		return AgentCard{}, 0, fmt.Errorf("no suitable agent found for query: %s", query)
	}

	best := results[0]
	h.logger.Info("a2a: semantic routing",
		"query", query,
		"agent", best.Agent.Name,
		"score", fmt.Sprintf("%.3f", best.Score),
	)

	return best.Agent, best.Score, nil
}

func (h *Handler) handleMessageSend(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest, targetAgentID string) {
	var params MessageSendParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, -32602, "invalid params")
		return
	}

	if params.ID == "" {
		params.ID = uuid.New().String()
	}

	ctx, span := telemetry.Tracer().Start(ctx, "a2a.gateway.request",
		trace.WithAttributes(
			attribute.String("a2a.method", req.Method),
			attribute.String("a2a.task.id", params.ID),
		),
	)
	defer span.End()

	// Route to agent
	agent, score, err := h.routeToAgent(ctx, targetAgentID, params)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		writeRPCError(w, req.ID, -32000, err.Error())
		return
	}

	span.SetAttributes(
		attribute.String("a2a.routing.agent", agent.Name),
		attribute.Float64("a2a.routing.score", score),
	)

	result, err := h.dispatchToAgent(ctx, agent, params)
	if err != nil {
		h.logger.Error("a2a: dispatch failed", "agent", agent.Name, "error", err)
		task := &Task{
			ID:     params.ID,
			Status: TaskStatus{State: TaskStateFailed, Message: &Message{Role: "agent", Parts: []Part{{Type: "text", Text: err.Error()}}}},
		}
		h.storeTask(task, agent.ID)
		writeRPCResult(w, req.ID, task)
		return
	}

	h.storeTask(result, agent.ID)
	writeRPCResult(w, req.ID, result)
}

func (h *Handler) handleMessageStream(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest, targetAgentID string) {
	var params MessageSendParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, -32602, "invalid params")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeRPCError(w, req.ID, -32000, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if params.ID == "" {
		params.ID = uuid.New().String()
	}

	ctx, span := telemetry.Tracer().Start(ctx, "a2a.gateway.stream",
		trace.WithAttributes(
			attribute.String("a2a.method", req.Method),
			attribute.String("a2a.task.id", params.ID),
		),
	)
	defer span.End()

	// Send initial working status
	task := &Task{
		ID:     params.ID,
		Status: TaskStatus{State: TaskStateWorking},
	}
	sendSSE(w, flusher, "status", task)

	// Route to agent
	agent, score, err := h.routeToAgent(ctx, targetAgentID, params)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		task.Status = TaskStatus{State: TaskStateFailed, Message: &Message{Role: "agent", Parts: []Part{{Type: "text", Text: err.Error()}}}}
		h.storeTask(task, "")
		sendSSE(w, flusher, "status", task)
		return
	}

	span.SetAttributes(
		attribute.String("a2a.routing.agent", agent.Name),
		attribute.Float64("a2a.routing.score", score),
	)

	// Dispatch
	result, err := h.dispatchToAgent(ctx, agent, params)
	if err != nil {
		task.Status = TaskStatus{State: TaskStateFailed, Message: &Message{Role: "agent", Parts: []Part{{Type: "text", Text: err.Error()}}}}
		h.storeTask(task, agent.ID)
		sendSSE(w, flusher, "status", task)
		return
	}

	h.storeTask(result, agent.ID)
	sendSSE(w, flusher, "status", result)
}

func (h *Handler) handleTaskGet(w http.ResponseWriter, req JSONRPCRequest) {
	var params TaskQueryParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, -32602, "invalid params")
		return
	}

	h.mu.RLock()
	entry, ok := h.tasks[params.ID]
	h.mu.RUnlock()

	if !ok {
		writeRPCError(w, req.ID, -32001, "task not found")
		return
	}
	writeRPCResult(w, req.ID, entry.task)
}

func (h *Handler) handleTaskCancel(w http.ResponseWriter, req JSONRPCRequest) {
	var params TaskQueryParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, -32602, "invalid params")
		return
	}

	h.mu.Lock()
	entry, ok := h.tasks[params.ID]
	if ok {
		entry.task.Status.State = TaskStateCanceled
	}
	h.mu.Unlock()

	if !ok {
		writeRPCError(w, req.ID, -32001, "task not found")
		return
	}
	writeRPCResult(w, req.ID, entry.task)
}

func (h *Handler) handleTaskList(w http.ResponseWriter, req JSONRPCRequest) {
	var params TaskListParams
	// params are optional for tasks/list
	if req.Params != nil {
		json.Unmarshal(req.Params, &params)
	}

	h.mu.RLock()
	var tasks []*Task
	for _, entry := range h.tasks {
		if params.State == "" || entry.task.Status.State == params.State {
			tasks = append(tasks, entry.task)
		}
	}
	h.mu.RUnlock()

	if tasks == nil {
		tasks = []*Task{}
	}
	writeRPCResult(w, req.ID, tasks)
}

func (h *Handler) dispatchToAgent(ctx context.Context, agent AgentCard, params MessageSendParams) (*Task, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "a2a.dispatch",
		trace.WithAttributes(
			attribute.String("a2a.agent.name", agent.Name),
			attribute.String("a2a.agent.id", agent.ID),
			attribute.String("a2a.task.id", params.ID),
		),
	)
	defer span.End()

	start := time.Now()

	reqBody, _ := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "message/send",
		ID:      1,
		Params:  mustMarshal(params),
	})

	agentURL := strings.TrimRight(agent.URL, "/") + "/a2a"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, agentURL, bytes.NewReader(reqBody))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("create request for %s: %w", agent.Name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(httpReq)
	latency := time.Since(start)

	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("dispatch to %s: %w", agent.Name, err)
	}
	defer resp.Body.Close()

	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("decode response from %s: %w", agent.Name, err)
	}

	if rpcResp.Error != nil {
		span.SetStatus(codes.Error, rpcResp.Error.Message)
		return nil, fmt.Errorf("agent %s error: %s", agent.Name, rpcResp.Error.Message)
	}

	// Parse result as Task
	taskBytes, _ := json.Marshal(rpcResp.Result)
	var task Task
	if err := json.Unmarshal(taskBytes, &task); err != nil {
		return nil, fmt.Errorf("parse task from %s: %w", agent.Name, err)
	}

	if task.ID == "" {
		task.ID = params.ID
	}

	span.SetAttributes(
		attribute.Int64("a2a.dispatch.latency_ms", latency.Milliseconds()),
		attribute.String("a2a.task.state", task.Status.State),
	)

	h.logger.Info("a2a: dispatch completed",
		"agent", agent.Name,
		"task_id", task.ID,
		"state", task.Status.State,
		"latency_ms", latency.Milliseconds(),
	)

	return &task, nil
}

func (h *Handler) storeTask(task *Task, agentID string) {
	h.mu.Lock()
	h.tasks[task.ID] = &taskEntry{task: task, agentID: agentID, createdAt: time.Now()}
	h.mu.Unlock()
}

func writeRPCResult(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func writeRPCError(w http.ResponseWriter, id any, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: message},
	})
}

func sendSSE(w http.ResponseWriter, flusher http.Flusher, event string, data any) {
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, jsonData)
	flusher.Flush()
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
