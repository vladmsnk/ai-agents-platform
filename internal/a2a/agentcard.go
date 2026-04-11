package a2a

import "encoding/json"

// Agent status constants.
const (
	StatusActive    = "active"
	StatusUnhealthy = "unhealthy"
)

// A2A v1.0 task state constants.
const (
	TaskStateSubmitted     = "submitted"
	TaskStateWorking       = "working"
	TaskStateCompleted     = "completed"
	TaskStateFailed        = "failed"
	TaskStateCanceled      = "canceled"
	TaskStateInputRequired = "input_required"
)

// AgentCard represents a Google A2A Agent Card.
// See: https://google.github.io/A2A/specification/
type AgentCard struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Description        string          `json:"description"`
	URL                string          `json:"url"`
	Version            string          `json:"version"`
	Capabilities       Capabilities    `json:"capabilities"`
	Skills             []Skill         `json:"skills"`
	DefaultInputModes  []string        `json:"defaultInputModes"`
	DefaultOutputModes []string        `json:"defaultOutputModes"`
	Authentication     *Authentication `json:"authentication,omitempty"`
	ProviderName       string          `json:"provider_name,omitempty"`
	Status             string          `json:"status"`
	Embedding          []float64       `json:"-"` // never exposed via API
}

type Capabilities struct {
	Streaming            bool `json:"streaming"`
	PushNotifications    bool `json:"pushNotifications"`
	StateTransitionHistory bool `json:"stateTransitionHistory"`
}

type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
}

type Authentication struct {
	Schemes []AuthScheme `json:"schemes"`
}

type AuthScheme struct {
	Scheme string `json:"scheme"` // "bearer", "api_key", "oauth2", etc.
}

// --- JSON-RPC types for A2A protocol ---

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	ID      any             `json:"id"`
	Params  json.RawMessage `json:"params"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      any         `json:"id"`
	Result  any         `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Task represents an A2A task.
type Task struct {
	ID       string       `json:"id"`
	Status   TaskStatus   `json:"status"`
	Artifacts []Artifact  `json:"artifacts,omitempty"`
}

type TaskStatus struct {
	State   string    `json:"state"` // "submitted", "working", "completed", "failed", "canceled"
	Message *Message  `json:"message,omitempty"`
}

type Message struct {
	Role  string `json:"role"` // "user" or "agent"
	Parts []Part `json:"parts"`
}

type Part struct {
	Type string `json:"type"` // "text", "data", "file"
	Text string `json:"text,omitempty"`
}

type Artifact struct {
	Name  string `json:"name,omitempty"`
	Parts []Part `json:"parts"`
}

// MessageSendParams is the params for message/send (v1.0) and tasks/send (v0.3).
type MessageSendParams struct {
	ID      string  `json:"id"`
	Message Message `json:"message"`
}

// TaskSendParams is an alias for backward compatibility with v0.3.
type TaskSendParams = MessageSendParams

// TaskQueryParams is the params for tasks/get.
type TaskQueryParams struct {
	ID string `json:"id"`
}

// TaskListParams is the params for tasks/list (v1.0).
type TaskListParams struct {
	State string `json:"state,omitempty"`
}
