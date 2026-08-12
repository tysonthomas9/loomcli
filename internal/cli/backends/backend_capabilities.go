package backends

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backendapi"
)

// StreamingBackend is an optional interface that backends can implement to
// support streaming responses. Use type assertion or InspectCapabilities to
// check whether a Backend supports this.
type StreamingBackend = backendapi.StreamingBackend

// SessionAwareBackend is an optional interface for backends that support
// resuming or continuing a previous agent session.
type SessionAwareBackend = backendapi.SessionAwareBackend

// ToolAwareBackend is an optional interface for backends that support
// restricting which tools the agent may or may not use.
type ToolAwareBackend = backendapi.ToolAwareBackend

// HealthCheckableBackend is an optional interface for backends that can
// report their installation and readiness status.
type HealthCheckableBackend = backendapi.HealthCheckableBackend

// ConfigurableBackend is an optional interface for backends that expose
// runtime-configurable options.
type ConfigurableBackend = backendapi.ConfigurableBackend

// MetadataProvider is an optional interface for backends that can report
// descriptive metadata about themselves.
type MetadataProvider = backendapi.MetadataProvider

// HealthStatus describes the health and readiness of a backend.
type HealthStatus = backendapi.HealthStatus

// BackendOption describes a single configurable option for a backend.
type BackendOption = backendapi.BackendOption

// BackendMeta contains descriptive metadata about a backend.
type BackendMeta = backendapi.BackendMeta

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
	Health    HealthCheckableBackend
	Config    ConfigurableBackend
	Meta      MetadataProvider
}

// VersionProbeTimeout bounds a single "<binary> --version" invocation.
//
// It deliberately matches harness-wrapper's own probe bound (also 20s):
// both spawn the same node-based CLIs, where a cold --version costs a
// second or two just to start node and considerably more on a loaded
// machine. harness-wrapper had to raise its bound from 2s after probe
// children were SIGKILLed mid-start under parallel load and reported as
// unknown versions; there is no reason for loom's copy of the same probe
// to relearn that. This is a hang guard, not a latency target.
//
// It is a var rather than a const only so tests can shrink it —
// production never assigns to it. Callers sizing a deadline that must
// outlast a probe should read this instead of re-deriving 20s of their
// own (see workspace ops ensure-runtime).
var VersionProbeTimeout = 20 * time.Second

// versionProbeWaitDelay caps how long Output() may linger after the
// probe context fires. Killing the child is not enough on its own: a
// grandchild that inherited the stdout pipe keeps the read blocked, so
// without this the bound above would not actually be a ceiling.
const versionProbeWaitDelay = 2 * time.Second

// detectBinaryVersion runs "<binary> --version" and returns the first line of
// output trimmed of whitespace. Returns "" if the binary is not found, the
// command fails, or the probe outlives VersionProbeTimeout.
//
// The bound is load-bearing rather than defensive. Nothing on the path from
// `loom workspace ops ensure-runtime` down to here carries a context: the
// ops deadline is only polled between iterations of the daemon wait loop,
// backendcheck.CheckBackend takes no context, and neither do the Meta() /
// HealthCheck() callers below. An unbounded --version on a wedged CLI
// therefore hangs the whole command with no deadline able to reach it.
func detectBinaryVersion(binary string) string {
	ctx, cancel := context.WithTimeout(context.Background(), VersionProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.WaitDelay = versionProbeWaitDelay
	out, err := cmd.Output()
	if err != nil {
		// A timeout surfaces as an error here, so it collapses into the
		// same "" that callers already treat as "version unknown".
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
	// Tool control is a static per-backend fact (which CLI flags exist), not
	// an optional Go interface: the old ToolAwareBackend interface shipped for
	// months with zero implementations while the config it implied was
	// silently ignored. SupportsToolControl reads the same capability table
	// ValidateSafetyKnobs enforces.
	caps.HasToolControl = SupportsToolControl(b.Name())
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
