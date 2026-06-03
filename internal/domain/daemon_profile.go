package domain

import "time"

// DaemonProfile is the per-workspace daemon configuration. 1:1 with
// Workspace — a default profile is created automatically when the
// workspace is created; callers Get + Upsert (no Create/Delete).
//
// Machine-local fields (filesystem PIDFile / LogDir paths) live here
// because workspace-scoped daemon installs may want to override defaults.
// Truly per-host bootstrap config (where loom finds fleet-db, OTLP
// endpoint) goes in env vars, never here.
type DaemonProfile struct {
	WorkspaceKey   string        `json:"workspace_key"`
	PIDFile        string        `json:"pid_file,omitempty"`
	LogDir         string        `json:"log_dir,omitempty"`
	EventsDir      string        `json:"events_dir,omitempty"`
	RestartPolicy  RestartPolicy `json:"restart_policy"`
	MaxAgents      *int          `json:"max_agents,omitempty"`
	IssueBackend   string        `json:"issue_backend,omitempty"`
	AgentBackend   string        `json:"agent_backend,omitempty"`
	StartupTimeout *int          `json:"startup_timeout,omitempty"`
	OTel           *OTelSettings `json:"otel,omitempty"`

	// FlueSandbox selects where flue-backed agents execute: "" / "local"
	// (host worktree) or "daytona" (a fresh remote Daytona sandbox per task,
	// patch-synced back). Injected as LOOM_FLUE_SANDBOX into spawned agents.
	FlueSandbox string `json:"flue_sandbox,omitempty"`

	UpdatedAt time.Time `json:"updated_at"`
}

// RestartPolicy controls how the supervisor restarts failed agents,
// throttles rate-limited retries, and times out unresponsive processes.
// All fields use pointer types so "unset" is distinguishable from "zero".
type RestartPolicy struct {
	MaxRetries       *int  `json:"max_retries,omitempty"`
	BackoffInitial   *int  `json:"backoff_initial,omitempty"`     // seconds
	BackoffMax       *int  `json:"backoff_max,omitempty"`         // seconds
	OutputTimeout    *int  `json:"output_timeout,omitempty"`      // seconds; 0 = disabled
	RateLimitBackoff *int  `json:"rate_limit_backoff,omitempty"`  // seconds
	RateLimitMaxWait *int  `json:"rate_limit_max_wait,omitempty"` // seconds
	RateLimitNoCount *bool `json:"rate_limit_no_count,omitempty"`
	TimeoutBackoff   *int  `json:"timeout_backoff,omitempty"`    // seconds
	NoWorkBackoff    *int  `json:"no_work_backoff,omitempty"`    // seconds
	IdlePollInterval *int  `json:"idle_poll_interval,omitempty"` // seconds
	YieldTimeout     *int  `json:"yield_timeout,omitempty"`      // seconds
	SigtermTimeout   *int  `json:"sigterm_timeout,omitempty"`    // seconds
}

// OTelSettings configures the OpenTelemetry exporter on the daemon.
// When nil on a DaemonProfile, telemetry is disabled.
type OTelSettings struct {
	Enabled         bool    `json:"enabled,omitempty"`
	Endpoint        string  `json:"endpoint,omitempty"`
	Protocol        string  `json:"protocol,omitempty"`
	ServiceName     string  `json:"service_name,omitempty"`
	SampleRate      float64 `json:"sample_rate,omitempty"`
	FlushIntervalMs int     `json:"flush_interval_ms,omitempty"`
	Traces          *bool   `json:"traces,omitempty"`
	Metrics         *bool   `json:"metrics,omitempty"`
}
