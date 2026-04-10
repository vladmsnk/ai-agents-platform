package a2a

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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

	switch req.Method {
	case "tasks/send":
		h.handleTaskSend(w, req)
	case "tasks/get":
		h.handleTaskGet(w, req)
	case "tasks/cancel":
		h.handleTaskCancel(w, req)
	case "tasks/sendSubscribe":
		h.handleTaskSendSubscribe(w, r, req)
	default:
		writeRPCError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (h *Handler) handleTaskSend(w http.ResponseWriter, req JSONRPCRequest) {
	var params TaskSendParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, -32602, "invalid params")
		return
	}

	if params.ID == "" {
		params.ID = uuid.New().String()
	}

	// Find an agent to handle the task
	agents := h.registry.List()
	if len(agents) == 0 {
		writeRPCError(w, req.ID, -32000, "no agents registered")
		return
	}

	// For now, dispatch to the first available agent
	agent := agents[0]
	result, err := h.dispatchToAgent(agent, params)
	if err != nil {
		h.logger.Error("a2a: dispatch failed", "agent", agent.Name, "error", err)
		task := &Task{
			ID:     params.ID,
			Status: TaskStatus{State: "failed", Message: &Message{Role: "agent", Parts: []Part{{Type: "text", Text: err.Error()}}}},
		}
		h.storeTask(task)
		writeRPCResult(w, req.ID, task)
		return
	}

	h.storeTask(result)
	writeRPCResult(w, req.ID, result)
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
		entry.task.Status.State = "canceled"
	}
	h.mu.Unlock()

	if !ok {
		writeRPCError(w, req.ID, -32001, "task not found")
		return
	}
	writeRPCResult(w, req.ID, entry.task)
}

func (h *Handler) handleTaskSendSubscribe(w http.ResponseWriter, r *http.Request, req JSONRPCRequest) {
	var params TaskSendParams
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

	// Send initial status
	task := &Task{
		ID:     params.ID,
		Status: TaskStatus{State: "working"},
	}
	h.storeTask(task)
	sendSSE(w, flusher, "status", task)

	// Dispatch
	agents := h.registry.List()
	if len(agents) == 0 {
		task.Status = TaskStatus{State: "failed", Message: &Message{Role: "agent", Parts: []Part{{Type: "text", Text: "no agents registered"}}}}
		h.storeTask(task)
		sendSSE(w, flusher, "status", task)
		return
	}

	result, err := h.dispatchToAgent(agents[0], params)
	if err != nil {
		task.Status = TaskStatus{State: "failed", Message: &Message{Role: "agent", Parts: []Part{{Type: "text", Text: err.Error()}}}}
		h.storeTask(task)
		sendSSE(w, flusher, "status", task)
		return
	}

	h.storeTask(result)
	sendSSE(w, flusher, "status", result)
}

func (h *Handler) dispatchToAgent(agent AgentCard, params TaskSendParams) (*Task, error) {
	reqBody, _ := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tasks/send",
		ID:      1,
		Params:  mustMarshal(params),
	})

	url := strings.TrimRight(agent.URL, "/") + "/a2a"
	resp, err := h.client.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("dispatch to %s: %w", agent.Name, err)
	}
	defer resp.Body.Close()

	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode response from %s: %w", agent.Name, err)
	}

	if rpcResp.Error != nil {
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

	return &task, nil
}

func (h *Handler) storeTask(task *Task) {
	h.mu.Lock()
	h.tasks[task.ID] = &taskEntry{task: task, createdAt: time.Now()}
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
