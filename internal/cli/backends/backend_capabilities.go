package backends

import (
	"os/exec"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// StreamingBackend is an optional interface that backends can implement to
// support streaming responses. Use type assertion or InspectCapabilities to
// check whether a Backend supports this.
type StreamingBackend = cli.StreamingBackend

// SessionAwareBackend is an optional interface for backends that support
// resuming or continuing a previous agent session.
type SessionAwareBackend = cli.SessionAwareBackend

// ToolAwareBackend is an optional interface for backends that support
// restricting which tools the agent may or may not use.
type ToolAwareBackend = cli.ToolAwareBackend

// HealthCheckableBackend is an optional interface for backends that can
// report their installation and readiness status.
type HealthCheckableBackend = cli.HealthCheckableBackend

// ConfigurableBackend is an optional interface for backends that expose
// runtime-configurable options.
type ConfigurableBackend = cli.ConfigurableBackend

// MetadataProvider is an optional interface for backends that can report
// descriptive metadata about themselves.
type MetadataProvider = cli.MetadataProvider

// HealthStatus describes the health and readiness of a backend.
type HealthStatus = cli.HealthStatus

// BackendOption describes a single configurable option for a backend.
type BackendOption = cli.BackendOption

// BackendMeta contains descriptive metadata about a backend.
type BackendMeta = cli.BackendMeta

// BackendCapabilities reports which optional interfaces a Backend implements.
// Check the boolean flags (HasStreaming, HasSessions, etc.) or the typed
// fields (Streaming, Sessions, etc.) to determine available capabilities.
// Typed fields are nil when the corresponding capability is not supported.
type BackendCapabilities struct {
	HasStreaming   bool
	HasSessions    bool
	HasToolControl bool
	HasHealthCheck bool
	HasConfig      bool
	HasMeta        bool

	Streaming StreamingBackend
	Sessions  SessionAwareBackend
	Tools     ToolAwareBackend
	Health    HealthCheckableBackend
	Config    ConfigurableBackend
	Meta      MetadataProvider
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

	if s, ok := b.(StreamingBackend); ok {
		caps.HasStreaming = true
		caps.Streaming = s
	}
	if s, ok := b.(SessionAwareBackend); ok {
		caps.HasSessions = true
		caps.Sessions = s
	}
	if t, ok := b.(ToolAwareBackend); ok {
		caps.HasToolControl = true
		caps.Tools = t
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

	return caps
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
