package backends

import (
	"context"
	"io"
	"os/exec"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// StreamingBackend is an optional interface that backends can implement to
// support streaming responses. Use type assertion or InspectCapabilities to
// check whether a Backend supports this.
type StreamingBackend interface {
	InvokeStreaming(ctx context.Context, workDir, prompt, agentName string) (io.ReadCloser, error)
}

// ResumableStreamingBackend is an optional interface for backends whose
// provider requires the resume session ID on the streaming invocation itself.
type ResumableStreamingBackend interface {
	InvokeStreamingResumed(ctx context.Context, workDir, prompt, agentName, providerSessionID string) (io.ReadCloser, error)
}

// ResumableNonInteractiveBackend is an optional interface for backends whose
// provider requires the resume session ID on the non-interactive invocation.
type ResumableNonInteractiveBackend interface {
	InvokeNonInteractiveResumed(workDir, prompt, agentName, providerSessionID string, shutdown <-chan struct{}, collector *usage.Collector) error
}

// SessionAwareBackend is an optional interface for backends that support
// resuming or continuing a previous agent session.
type SessionAwareBackend interface {
	ContinueSession(workDir, sessionID, agentName string) error
	LastSessionID(workDir string) string
}

// ToolAwareBackend is an optional interface for backends that support
// restricting which tools the agent may or may not use.
type ToolAwareBackend interface {
	SetAllowedTools(tools []string)
	SetDeniedTools(tools []string)
}

// TypedToolRuntimeBackend is an optional interface for backends that can
// execute TypeScript-defined model tools through a trusted runtime boundary.
type TypedToolRuntimeBackend interface {
	SetTypedTools(tools []TypedToolDefinition) error
}

// TypedToolCallReporter is an optional interface for backends that can report
// model-visible TypeScript tool calls observed during the last invocation.
type TypedToolCallReporter interface {
	TypedToolCalls(workDir string) []TypedToolCallEvent
}

// ProviderMetadataReporter is an optional interface for backends that capture
// provider-native metadata during the last invocation.
type ProviderMetadataReporter interface {
	LastProviderMetadata(workDir string) map[string]any
}

// TypedToolDefinition describes one TypeScript-authored model-callable tool
// that a backend may expose to a provider.
type TypedToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Version     string         `json:"version,omitempty"`
	SourcePath  string         `json:"source_path,omitempty"`
	SourceHash  string         `json:"source_hash,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Handler     string         `json:"handler,omitempty"`
	Runtime     string         `json:"runtime,omitempty"`
	Repos       []string       `json:"repos,omitempty"`
	Env         []string       `json:"env,omitempty"`
	ReadOnly    bool           `json:"read_only,omitempty"`
}

// TypedToolCallEvent describes one model-visible typed tool call observed by a
// backend runtime. Backends should redact sensitive arguments/results before
// returning these events.
type TypedToolCallEvent struct {
	CallID              string         `json:"call_id,omitempty"`
	Name                string         `json:"name"`
	Status              string         `json:"status,omitempty"`
	Arguments           map[string]any `json:"arguments,omitempty"`
	Result              any            `json:"result,omitempty"`
	Error               string         `json:"error,omitempty"`
	StartedAt           string         `json:"started_at,omitempty"`
	CompletedAt         string         `json:"completed_at,omitempty"`
	DurationMS          int64          `json:"duration_ms,omitempty"`
	IdempotencyKey      string         `json:"idempotency_key,omitempty"`
	AuthorizationStatus string         `json:"authorization_status,omitempty"`
	Redacted            bool           `json:"redacted,omitempty"`
}

// HealthCheckableBackend is an optional interface for backends that can
// report their installation and readiness status.
type HealthCheckableBackend interface {
	HealthCheck() HealthStatus
}

// ConfigurableBackend is an optional interface for backends that expose
// runtime-configurable options.
type ConfigurableBackend interface {
	Options() []BackendOption
	SetOption(key, value string) error
	GetOption(key string) (string, error)
}

// MetadataProvider is an optional interface for backends that can report
// descriptive metadata about themselves.
type MetadataProvider interface {
	Meta() BackendMeta
}

// HealthStatus describes the health and readiness of a backend.
type HealthStatus struct {
	Healthy   bool   `json:"healthy"`
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	APIKeySet bool   `json:"api_key_set"`
	Message   string `json:"message"`
}

// BackendOption describes a single configurable option for a backend.
type BackendOption struct {
	Key          string `json:"key"`
	Description  string `json:"description"`
	Default      string `json:"default"`
	CurrentValue string `json:"current_value"`
}

// BackendMeta contains descriptive metadata about a backend.
type BackendMeta struct {
	DisplayName string `json:"display_name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	URL         string `json:"url"`
	BinaryName  string `json:"binary_name"`
}

// BackendCapabilities reports which optional interfaces a Backend implements.
// Check the boolean flags (HasStreaming, HasSessions, etc.) or the typed
// fields (Streaming, Sessions, etc.) to determine available capabilities.
// Typed fields are nil when the corresponding capability is not supported.
type BackendCapabilities struct {
	HasStreaming            bool
	HasStreamingResume      bool
	HasNonInteractiveResume bool
	HasSessions             bool
	HasToolControl          bool
	HasTypedToolRuntime     bool
	HasTypedToolCalls       bool
	HasProviderMetadata     bool
	HasHealthCheck          bool
	HasConfig               bool
	HasMeta                 bool

	Streaming            StreamingBackend
	StreamingResume      ResumableStreamingBackend
	NonInteractiveResume ResumableNonInteractiveBackend
	Sessions             SessionAwareBackend
	Tools                ToolAwareBackend
	TypedToolRuntime     TypedToolRuntimeBackend
	TypedToolCalls       TypedToolCallReporter
	ProviderMetadata     ProviderMetadataReporter
	Health               HealthCheckableBackend
	Config               ConfigurableBackend
	Meta                 MetadataProvider
}

// detectBinaryVersion runs "<binary> --version" and returns the first line of
// output trimmed of whitespace. Returns "" if the binary is not found or the
// command fails.
func detectBinaryVersion(binary string) string {
	out, err := exec.Command(binary, "--version").Output()
	if err != nil {
		return ""
	}
	line := strings.SplitN(string(out), "\n", 2)[0]
	return strings.TrimSpace(line)
}

// InspectCapabilities performs type assertions on b to discover which optional
// interfaces it implements and returns a BackendCapabilities summary.
// Returns a zero-value BackendCapabilities if b is the nil interface value.
// Note: a typed nil (e.g., (*ClaudeBackend)(nil)) is not caught by this
// guard; callers must not pass typed nils.
func InspectCapabilities(b cli.Backend) BackendCapabilities {
	if b == nil {
		return BackendCapabilities{}
	}

	var caps BackendCapabilities
	inspectStreamingCapabilities(b, &caps)
	inspectToolCapabilities(b, &caps)
	inspectBackendMetadataCapabilities(b, &caps)
	return caps
}

func inspectStreamingCapabilities(b cli.Backend, caps *BackendCapabilities) {
	if s, ok := b.(StreamingBackend); ok {
		caps.HasStreaming = true
		caps.Streaming = s
	}
	if s, ok := b.(ResumableStreamingBackend); ok {
		caps.HasStreamingResume = true
		caps.StreamingResume = s
	}
	if n, ok := b.(ResumableNonInteractiveBackend); ok {
		caps.HasNonInteractiveResume = true
		caps.NonInteractiveResume = n
	}
	if s, ok := b.(SessionAwareBackend); ok {
		caps.HasSessions = true
		caps.Sessions = s
	}
}

func inspectToolCapabilities(b cli.Backend, caps *BackendCapabilities) {
	if t, ok := b.(ToolAwareBackend); ok {
		caps.HasToolControl = true
		caps.Tools = t
	}
	if t, ok := b.(TypedToolRuntimeBackend); ok {
		caps.HasTypedToolRuntime = true
		caps.TypedToolRuntime = t
	}
	if t, ok := b.(TypedToolCallReporter); ok {
		caps.HasTypedToolCalls = true
		caps.TypedToolCalls = t
	}
}

func inspectBackendMetadataCapabilities(b cli.Backend, caps *BackendCapabilities) {
	if p, ok := b.(ProviderMetadataReporter); ok {
		caps.HasProviderMetadata = true
		caps.ProviderMetadata = p
	}
	if h, ok := b.(HealthCheckableBackend); ok {
		caps.HasHealthCheck = true
		caps.Health = h
	}
	if c, ok := b.(ConfigurableBackend); ok {
		caps.HasConfig = true
		caps.Config = c
	}
	if m, ok := b.(MetadataProvider); ok {
		caps.HasMeta = true
		caps.Meta = m
	}
}

// CheckBackendHealth returns the health status of the named backend.
// Returns (status, true) if the backend supports health checks, or
// (zero, false) if the backend is not registered or does not implement
// HealthCheckableBackend.
func CheckBackendHealth(name string) (HealthStatus, bool) {
	b, ok := cli.GetBackendByName(name)
	if !ok {
		return HealthStatus{}, false
	}
	caps := InspectCapabilities(b)
	if !caps.HasHealthCheck {
		return HealthStatus{}, false
	}
	return caps.Health.HealthCheck(), true
}
