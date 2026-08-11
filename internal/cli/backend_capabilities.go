package cli

import (
	"context"
	"io"
)

// StreamingBackend is the optional streaming surface implemented by coding
// backends that can expose their output as a reader.
type StreamingBackend interface {
	InvokeStreaming(ctx context.Context, workDir, prompt, agentName string) (io.ReadCloser, error)
}

type SessionAwareBackend interface {
	ContinueSession(workDir, sessionID, agentName string) error
	LastSessionID(workDir string) string
}

type ToolAwareBackend interface {
	SetAllowedTools(tools []string)
	SetDeniedTools(tools []string)
}

type HealthCheckableBackend interface {
	HealthCheck() HealthStatus
}

type ConfigurableBackend interface {
	Options() []BackendOption
	SetOption(key, value string) error
	GetOption(key string) (string, error)
}

type MetadataProvider interface {
	Meta() BackendMeta
}

type HealthStatus struct {
	Healthy   bool   `json:"healthy"`
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	APIKeySet bool   `json:"api_key_set"`
	Message   string `json:"message"`
}

type BackendOption struct {
	Key          string `json:"key"`
	Description  string `json:"description"`
	Default      string `json:"default"`
	CurrentValue string `json:"current_value"`
}

type BackendMeta struct {
	DisplayName string `json:"display_name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	URL         string `json:"url"`
	BinaryName  string `json:"binary_name"`
}

// StreamEvent and its nested wire types describe the common stream-json
// subset used by deterministic test backends. Concrete backends may retain
// richer private event types.
type StreamEvent struct {
	Type    string        `json:"type"`
	Subtype string        `json:"subtype,omitempty"`
	Message *EventMessage `json:"message,omitempty"`
	Usage   *StreamUsage  `json:"usage,omitempty"`
}

type EventMessage struct {
	ID    string       `json:"id,omitempty"`
	Usage *StreamUsage `json:"usage,omitempty"`
}

type StreamUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}
