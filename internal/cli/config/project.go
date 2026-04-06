package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

var projectConfigVersionWarnOnce sync.Once

func resetProjectConfigVersionWarnOnce() {
	projectConfigVersionWarnOnce = sync.Once{}
}

// DaemonSettings holds daemon-specific config fields.
type DaemonSettings struct {
	PIDFile        string            `yaml:"pid_file,omitempty"`
	LogDir         string            `yaml:"log_dir,omitempty"`
	EventsDir      string            `yaml:"events_dir,omitempty"`
	RestartPolicy  RestartPolicy     `yaml:"restart_policy,omitempty"`
	MaxAgents      *int              `yaml:"max_agents,omitempty"`
	RedisURL       string            `yaml:"redis_url,omitempty"` // stale-detector/serve Redis — NOT used by fleet-db (see FleetDBSettings.RedisURL)
	OTel           *OTelDaemonConfig `yaml:"otel,omitempty"`
	FleetDB        *FleetDBSettings  `yaml:"fleetdb,omitempty"`         // fleet-db backend config (separate from RedisURL above)
	IssueBackend   string            `yaml:"issue_backend,omitempty"`   // "beads" (default), "fleetdb", or "fleet"
	Fleet          *FleetSettings    `yaml:"fleet,omitempty"`           // fleet client config (remote fleet server connection)
	StartupTimeout *int              `yaml:"startup_timeout,omitempty"` // seconds; how long to wait for daemon readiness (default 30)
}

// GetStartupTimeout returns the configured startup timeout or the given fallback.
func (d *DaemonSettings) GetStartupTimeout(fallback time.Duration) time.Duration {
	if d == nil || d.StartupTimeout == nil || *d.StartupTimeout <= 0 {
		return fallback
	}
	return time.Duration(*d.StartupTimeout) * time.Second
}

// OTelDaemonConfig holds OpenTelemetry export configuration for the daemon.
type OTelDaemonConfig struct {
	Enabled         bool    `yaml:"enabled,omitempty"`
	Endpoint        string  `yaml:"endpoint,omitempty"`
	Protocol        string  `yaml:"protocol,omitempty"`
	ServiceName     string  `yaml:"service_name,omitempty"`
	SampleRate      float64 `yaml:"sample_rate,omitempty"`
	FlushIntervalMs int     `yaml:"flush_interval_ms,omitempty"`
	Traces          *bool   `yaml:"traces,omitempty"`
	Metrics         *bool   `yaml:"metrics,omitempty"`
}

// RestartPolicy defines how the daemon restarts failed agents.
type RestartPolicy struct {
	MaxRetries       *int  `yaml:"max_retries,omitempty"`
	BackoffInitial   *int  `yaml:"backoff_initial,omitempty"`     // seconds
	BackoffMax       *int  `yaml:"backoff_max,omitempty"`         // seconds
	OutputTimeout    *int  `yaml:"output_timeout,omitempty"`      // seconds; kill agent after this long with no output (0 = disabled)
	RateLimitBackoff *int  `yaml:"rate_limit_backoff,omitempty"`  // seconds (default 30)
	RateLimitMaxWait *int  `yaml:"rate_limit_max_wait,omitempty"` // seconds (default 300)
	RateLimitNoCount *bool `yaml:"rate_limit_no_count,omitempty"` // default true: rate-limit retries don't count toward max_retries
	TimeoutBackoff   *int  `yaml:"timeout_backoff,omitempty"`     // seconds (default 5)
	NoWorkBackoff    *int  `yaml:"no_work_backoff,omitempty"`     // seconds (default 30); fixed interval when no tasks available
	IdlePollInterval *int  `yaml:"idle_poll_interval,omitempty"`  // seconds (default 30); polling interval for task availability
	YieldTimeout     *int  `yaml:"yield_timeout,omitempty"`       // seconds; how long to wait for agent to yield before SIGTERM (default 60)
	SigtermTimeout   *int  `yaml:"sigterm_timeout,omitempty"`     // seconds; SIGTERM→SIGKILL window (default 300)
}

// RoleConfig defines an agent role (built-in like "plan"/"task", or custom).
type RoleConfig struct {
	Description    string   `yaml:"description,omitempty"`
	PromptFile     string   `yaml:"prompt_file,omitempty"`
	Model          string   `yaml:"model,omitempty"`
	TaskFilter     string   `yaml:"task_filter,omitempty"`
	Backend        string   `yaml:"backend,omitempty"`
	PathPatterns   []string `yaml:"path_patterns,omitempty"`
	Skills         []string `yaml:"skills,omitempty"`
	MaxPriority    *int     `yaml:"max_priority,omitempty"`
	MaxConcurrency *int     `yaml:"max_concurrency,omitempty"`
	ReadOnly       bool     `yaml:"read_only,omitempty"`
	AllowedTools   []string `yaml:"allowed_tools,omitempty"`
	DeniedTools    []string `yaml:"denied_tools,omitempty"`
}

// AgentEntry defines a single agent assignment.
//
// Multi-repo routing fields:
//
//	repos: ["backend", "frontend"]         # explicit repo names this agent handles
//	repo_groups: ["infra", "data"]         # bind to groups defined in RepoConfig
//	cross_repo: true                       # agent can pick up tasks spanning repos
//
// An agent with neither repos nor repo_groups can work on any repo (backward compatible).
type AgentEntry struct {
	Worktree         string   `yaml:"worktree"`
	Role             string   `yaml:"role"`
	Repo             string   `yaml:"repo,omitempty"`
	Auto             bool     `yaml:"auto,omitempty"`
	Backend          string   `yaml:"backend,omitempty"`
	FallbackBackends []string `yaml:"fallback_backends,omitempty"`
	PathPatterns     []string `yaml:"path_patterns,omitempty"`
	SourceRepos      []string `yaml:"-" json:"-"` // resolved repo IDs; env-only transport, not persisted in YAML
	Repos            []string `yaml:"repos,omitempty"`
	RepoGroups       []string `yaml:"repo_groups,omitempty"`
	CrossRepo        bool     `yaml:"cross_repo,omitempty"`
	Parent           string   `yaml:"parent,omitempty"` // epic ID to scope this agent to; empty = no epic assignment
}

// Equal compares persisted config fields only (excludes SourceRepos). Update when adding fields.
func (a AgentEntry) Equal(b AgentEntry) bool {
	return a.Worktree == b.Worktree && a.Role == b.Role && a.Repo == b.Repo &&
		a.Auto == b.Auto && a.Backend == b.Backend && a.CrossRepo == b.CrossRepo && a.Parent == b.Parent &&
		slices.Equal(a.FallbackBackends, b.FallbackBackends) && slices.Equal(a.PathPatterns, b.PathPatterns) &&
		slices.Equal(a.Repos, b.Repos) && slices.Equal(a.RepoGroups, b.RepoGroups)
}

// ProjectFile represents the project-local loom.yaml.
type ProjectFile struct {
	Version int                   `yaml:"version,omitempty"`
	Backend string                `yaml:"backend,omitempty"`
	Daemon  *DaemonSettings       `yaml:"daemon,omitempty"`
	Roles   map[string]RoleConfig `yaml:"roles,omitempty"`
	Agents  []AgentEntry          `yaml:"agents,omitempty"`
}

// DaemonConfig is the merged, resolved configuration used by callers.
type DaemonConfig struct {
	Backend string                `yaml:"backend,omitempty"`
	Daemon  DaemonSettings        `yaml:"daemon,omitempty"`
	Roles   map[string]RoleConfig `yaml:"roles,omitempty"`
	Agents  []AgentEntry          `yaml:"agents,omitempty"`
}

// LoadProjectFile reads and parses the project-local loom.yaml from dir.
// Returns (nil, nil) if the file does not exist.
func LoadProjectFile(dir string) (*ProjectFile, error) {
	path := filepath.Join(dir, "loom.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading project file %s: %w", path, err)
	}

	data, err = ExpandConfigBytes(data)
	if err != nil {
		return nil, fmt.Errorf("expanding env vars in %s: %w", path, err)
	}

	resolver := NewSecretResolver()
	data, err = ResolveSecretsInBytes(data, resolver)
	if err != nil {
		return nil, fmt.Errorf("resolving secrets in %s: %w", path, err)
	}

	var pf ProjectFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parsing project file %s: %w", path, err)
	}
	if pf.Version < CurrentConfigVersion {
		projectConfigVersionWarnOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "Warning: project config %s is version %d (current: %d). Run 'loom config migrate' to upgrade.\n", path, pf.Version, CurrentConfigVersion)
		})
	}
	return &pf, nil
}

// LoadDaemonConfig merges global (~/.loom/config.yaml) and local (loom.yaml) config.
// Local values override global. Returns defaults if neither file exists.
func LoadDaemonConfig(projectDir string) (*DaemonConfig, error) {
	// Load global config
	globalCfg, err := LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("loading global config: %w", err)
	}

	// Load project-local config
	projectFile, err := LoadProjectFile(projectDir)
	if err != nil {
		return nil, fmt.Errorf("loading project config: %w", err)
	}

	// Start with defaults
	dc := &DaemonConfig{
		Daemon: DaemonSettings{
			PIDFile:   ".loom/daemon.pid",
			LogDir:    ".loom/logs",
			EventsDir: ".loom/events",
			RestartPolicy: RestartPolicy{
				MaxRetries:     IntPtr(3),
				BackoffInitial: IntPtr(2),
				BackoffMax:     IntPtr(300),
				OutputTimeout:  IntPtr(900), // 15 minutes
			},
			MaxAgents: IntPtr(20),
		},
		Roles: make(map[string]RoleConfig),
	}

	// Overlay global backend setting
	if globalCfg != nil && globalCfg.Backend != "" {
		dc.Backend = globalCfg.Backend
	}

	// Overlay global daemon settings
	if globalCfg != nil && globalCfg.Daemon != nil {
		overlayDaemonSettings(&dc.Daemon, globalCfg.Daemon)
	}

	// Overlay local daemon settings (local wins)
	if projectFile != nil {
		if projectFile.Backend != "" {
			dc.Backend = projectFile.Backend
		}
		if projectFile.Daemon != nil {
			overlayDaemonSettings(&dc.Daemon, projectFile.Daemon)
		}

		// Merge roles: local replaces entire role entry by key
		for k, v := range projectFile.Roles {
			dc.Roles[k] = v
		}

		// Agents come from local only
		dc.Agents = projectFile.Agents
	}

	if err := validateAgents(dc.Agents, dc.Daemon.MaxAgents); err != nil {
		return nil, err
	}

	// Validate repo references against workspace config
	if err := validateAgentRepos(dc.Agents); err != nil {
		return nil, err
	}

	// Run comprehensive validation if a validator is registered.
	if projectConfigValidator != nil {
		if err := projectConfigValidator(dc, projectDir); err != nil {
			return nil, err
		}
	}

	return dc, nil
}

// validateAgents checks that agent entries and max_agents limits are valid.
func validateAgents(agents []AgentEntry, maxAgents *int) error {
	for i, a := range agents {
		if a.Worktree == "" {
			return fmt.Errorf("agent[%d]: worktree is required", i)
		}
		if a.Role == "" {
			return fmt.Errorf("agent[%d]: role is required", i)
		}
		for j, fb := range a.FallbackBackends {
			if fb == "" {
				return fmt.Errorf("agent[%d]: fallback_backends[%d] is empty", i, j)
			}
		}
	}
	if maxAgents != nil && *maxAgents < 0 {
		return fmt.Errorf("max_agents must be non-negative, got %d", *maxAgents)
	}
	if maxAgents != nil && *maxAgents > 0 && len(agents) > *maxAgents {
		return fmt.Errorf("too many agents configured: %d exceeds max_agents limit of %d", len(agents), *maxAgents)
	}
	return nil
}

// validateAgentRepos checks that agent Repo fields reference valid repos in the workspace config.
// In workspace mode, unknown repo names are hard errors. Outside workspace mode, Repo fields
// trigger a warning but are not blocking.
func validateAgentRepos(agents []AgentEntry) error {
	// Check if any agent uses Repo
	hasRepo := false
	for _, a := range agents {
		if a.Repo != "" {
			hasRepo = true
			break
		}
	}
	if !hasRepo {
		return nil
	}

	ws, err := ResolveActiveWorkspace()
	if err != nil {
		return fmt.Errorf("validating agent repos: %w", err)
	}

	if ws == nil {
		// Not in workspace mode — warn but don't fail
		fmt.Fprintf(os.Stderr, "Warning: agent(s) declare repo but no workspace is configured; repo field will be ignored\n")
		return nil
	}

	// Build set of valid repo names
	repoNames := make(map[string]bool, len(ws.Repos))
	for _, r := range ws.Repos {
		repoNames[r.Name] = true
	}

	for i, a := range agents {
		if a.Repo == "" {
			continue
		}
		if !repoNames[a.Repo] {
			available := make([]string, 0, len(ws.Repos))
			for _, r := range ws.Repos {
				available = append(available, r.Name)
			}
			return fmt.Errorf("agent[%d]: repo %q not found in workspace; available repos: %v", i, a.Repo, available)
		}
	}
	return nil
}

// resolveRepoPath looks up a repo by name in the active workspace config and returns
// its absolute path. Returns an error if the repo is not found or the path doesn't exist.
func resolveRepoPath(repoName string) (string, error) {
	ws, err := ResolveActiveWorkspace()
	if err != nil {
		return "", fmt.Errorf("resolving workspace: %w", err)
	}
	if ws == nil {
		return "", fmt.Errorf("no active workspace configured")
	}

	for _, repo := range ws.Repos {
		if repo.Name == repoName {
			absPath := repo.ResolveAbsPath(ws.Path)
			if info, err := os.Stat(absPath); err != nil {
				return "", fmt.Errorf("repo path %q does not exist: %w", absPath, err)
			} else if !info.IsDir() {
				return "", fmt.Errorf("repo path %q is not a directory", absPath)
			}
			return absPath, nil
		}
	}
	return "", fmt.Errorf("repo %q not found in workspace", repoName)
}

// overlayDaemonSettings applies explicitly-set values from src onto dst.
func overlayDaemonSettings(dst *DaemonSettings, src *DaemonSettings) {
	if src.PIDFile != "" {
		dst.PIDFile = src.PIDFile
	}
	if src.LogDir != "" {
		dst.LogDir = src.LogDir
	}
	if src.EventsDir != "" {
		dst.EventsDir = src.EventsDir
	}
	overlayRestartPolicy(&dst.RestartPolicy, &src.RestartPolicy)
	if src.MaxAgents != nil {
		dst.MaxAgents = src.MaxAgents
	}
	if src.RedisURL != "" {
		dst.RedisURL = src.RedisURL
	}
	if src.OTel != nil {
		if dst.OTel == nil {
			dst.OTel = &OTelDaemonConfig{}
		}
		overlayOTelConfig(dst.OTel, src.OTel)
	}
	if src.FleetDB != nil {
		if dst.FleetDB == nil {
			dst.FleetDB = &FleetDBSettings{}
		}
		overlayFleetDBSettings(dst.FleetDB, src.FleetDB)
	}
	if src.IssueBackend != "" {
		dst.IssueBackend = src.IssueBackend
	}
	if src.Fleet != nil {
		if dst.Fleet == nil {
			dst.Fleet = &FleetSettings{}
		}
		overlayFleetSettings(dst.Fleet, src.Fleet)
	}
	if src.StartupTimeout != nil {
		dst.StartupTimeout = src.StartupTimeout
	}
}

func overlayRestartPolicy(dst, src *RestartPolicy) {
	if src.MaxRetries != nil {
		dst.MaxRetries = src.MaxRetries
	}
	if src.BackoffInitial != nil {
		dst.BackoffInitial = src.BackoffInitial
	}
	if src.BackoffMax != nil {
		dst.BackoffMax = src.BackoffMax
	}
	if src.OutputTimeout != nil {
		dst.OutputTimeout = src.OutputTimeout
	}
	if src.RateLimitBackoff != nil {
		dst.RateLimitBackoff = src.RateLimitBackoff
	}
	if src.RateLimitMaxWait != nil {
		dst.RateLimitMaxWait = src.RateLimitMaxWait
	}
	if src.RateLimitNoCount != nil {
		dst.RateLimitNoCount = src.RateLimitNoCount
	}
	if src.TimeoutBackoff != nil {
		dst.TimeoutBackoff = src.TimeoutBackoff
	}
	if src.NoWorkBackoff != nil {
		dst.NoWorkBackoff = src.NoWorkBackoff
	}
	if src.IdlePollInterval != nil {
		dst.IdlePollInterval = src.IdlePollInterval
	}
	if src.YieldTimeout != nil {
		dst.YieldTimeout = src.YieldTimeout
	}
	if src.SigtermTimeout != nil {
		dst.SigtermTimeout = src.SigtermTimeout
	}
}

func overlayOTelConfig(dst, src *OTelDaemonConfig) {
	if src.Enabled {
		dst.Enabled = true
	}
	if src.Endpoint != "" {
		dst.Endpoint = src.Endpoint
	}
	if src.Protocol != "" {
		dst.Protocol = src.Protocol
	}
	if src.ServiceName != "" {
		dst.ServiceName = src.ServiceName
	}
	if src.SampleRate != 0 {
		dst.SampleRate = src.SampleRate
	}
	if src.FlushIntervalMs != 0 {
		dst.FlushIntervalMs = src.FlushIntervalMs
	}
	if src.Traces != nil {
		dst.Traces = src.Traces
	}
	if src.Metrics != nil {
		dst.Metrics = src.Metrics
	}
}

func IntPtr(v int) *int    { return &v }
func BoolPtr(v bool) *bool { return &v }

// ResolveRole looks up a role by name in the merged config.
// Returns the RoleConfig and true if found, zero value and false if not.
func (dc *DaemonConfig) ResolveRole(name string) (RoleConfig, bool) {
	rc, ok := dc.Roles[name]
	return rc, ok
}

// resolvePath resolves a path relative to baseDir, or returns as-is if absolute.
func resolvePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}

// ResolveDaemonStatePath returns the path to daemon-agents.json for the given project directory.
// It loads the daemon config to determine the PID file location, then returns the state file
// path adjacent to the PID file. On config load error, falls back to <projectDir>/.loom/daemon-agents.json.
func ResolveDaemonStatePath(projectDir string) string {
	config, err := LoadDaemonConfig(projectDir)
	if err != nil {
		return filepath.Join(projectDir, ".loom", "daemon-agents.json")
	}
	pidFilePath := resolvePath(projectDir, config.Daemon.PIDFile)
	return filepath.Join(filepath.Dir(pidFilePath), "daemon-agents.json")
}
