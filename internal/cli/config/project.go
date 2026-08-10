package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// DaemonSettings holds daemon-specific config fields.
type DaemonSettings struct {
	PIDFile        string            `yaml:"pid_file,omitempty"`
	LogDir         string            `yaml:"log_dir,omitempty"`
	EventsDir      string            `yaml:"events_dir,omitempty"`
	RestartPolicy  RestartPolicy     `yaml:"restart_policy,omitempty"`
	MaxAgents      *int              `yaml:"max_agents,omitempty"`
	RedisURL       string            `yaml:"redis_url,omitempty"` // stale-detector/serve Redis
	OTel           *OTelDaemonConfig `yaml:"otel,omitempty"`
	IssueBackend   string            `yaml:"issue_backend,omitempty"`   // "fleetdb", "fleet", or "api"
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
	Kind         string   `yaml:"kind,omitempty"`
	Description  string   `yaml:"description,omitempty"`
	Prompt       string   `yaml:"prompt,omitempty"`
	PromptFile   string   `yaml:"prompt_file,omitempty"`
	Model        string   `yaml:"model,omitempty"`
	TaskFilter   string   `yaml:"task_filter,omitempty"`
	Backend      string   `yaml:"backend,omitempty"`
	Effort       string   `yaml:"effort,omitempty"`
	PathPatterns []string `yaml:"path_patterns,omitempty"`
	Skills       []string `yaml:"skills,omitempty"`
	// InputPolicy governs which harness prompts an agent in this role may
	// auto-answer. Nil denies every prompt — see domain.RoleInputPolicy for
	// why the unset case has to be the restrictive one.
	InputPolicy    *domain.RoleInputPolicy `yaml:"input_policy,omitempty"`
	MaxPriority    *int                    `yaml:"max_priority,omitempty"`
	MaxConcurrency *int                    `yaml:"max_concurrency,omitempty"`
	ReadOnly       bool                    `yaml:"read_only,omitempty"`
	AllowedTools   []string                `yaml:"allowed_tools,omitempty"`
	DeniedTools    []string                `yaml:"denied_tools,omitempty"`
	MaxBudgetUSD   *float64                `yaml:"max_budget_usd,omitempty"`
	// MaxRunDuration caps a single supervised run's wall-clock age, in
	// seconds. Nil inherits the daemon-wide default; <= 0 disables the cap for
	// this role. See supervisor/run_duration.go.
	MaxRunDuration *int `yaml:"max_run_duration,omitempty"`
}

// AgentEntry defines a single agent assignment.
//
// Multi-repo routing fields:
//
//	repos: ["backend", "frontend"]         # explicit repo names this agent handles
//	repo_groups: ["infra", "data"]         # bind to groups defined in RepoConfig
//	cross_repo: true                       # agent can pick up tasks spanning repos
//
// An agent with neither repos nor repo_groups can work on any repo.
type AgentEntry struct {
	Worktree         string                   `yaml:"worktree"`
	Role             string                   `yaml:"role"`
	Repo             string                   `yaml:"repo,omitempty"`
	Auto             bool                     `yaml:"auto,omitempty"`
	Backend          string                   `yaml:"backend,omitempty"`
	FallbackBackends []string                 `yaml:"fallback_backends,omitempty"`
	PathPatterns     []string                 `yaml:"path_patterns,omitempty"`
	SourceRepos      []string                 `yaml:"-" json:"-"` // resolved repo IDs; env-only transport, not persisted in YAML
	Repos            []string                 `yaml:"repos,omitempty"`
	RepoGroups       []string                 `yaml:"repo_groups,omitempty"`
	CrossRepo        bool                     `yaml:"cross_repo,omitempty"`
	Parent           string                   `yaml:"parent,omitempty"` // epic ID to scope this agent to; empty = no epic assignment
	Mode             domain.AgentMode         `yaml:"mode,omitempty"`   // ephemeral: exit cleanly after one successful task; service: loop forever (default)
	DesiredState     domain.AgentDesiredState `yaml:"desired_state,omitempty"`
	// Hooks are the supervisor-owned post-run pipelines. Nil preserves the
	// pre-hook behavior (the agent's own prompt does its bookkeeping).
	Hooks *domain.AgentHooks `yaml:"hooks,omitempty"`
}

// Equal compares persisted config fields only (excludes SourceRepos). Update when adding fields.
func (a AgentEntry) Equal(b AgentEntry) bool {
	return a.Worktree == b.Worktree && a.Role == b.Role && a.Repo == b.Repo &&
		a.Auto == b.Auto && a.Backend == b.Backend && a.CrossRepo == b.CrossRepo && a.Parent == b.Parent &&
		a.Mode == b.Mode &&
		a.DesiredState == b.DesiredState &&
		a.Hooks.Equal(b.Hooks) &&
		slices.Equal(a.FallbackBackends, b.FallbackBackends) && slices.Equal(a.PathPatterns, b.PathPatterns) &&
		slices.Equal(a.Repos, b.Repos) && slices.Equal(a.RepoGroups, b.RepoGroups)
}

// ShouldSupervise reports whether the local daemon should run this agent.
// Empty desired_state preserves legacy behavior for existing agent definitions.
func (a AgentEntry) ShouldSupervise() bool {
	if domain.IsInteractiveRoleName(a.Role) {
		return false
	}
	return a.shouldSuperviseByDesiredState()
}

// ShouldSuperviseWithRoles reports whether the local daemon should run this
// agent, using role kind metadata when the merged daemon config is available.
func (a AgentEntry) ShouldSuperviseWithRoles(roles map[string]RoleConfig) bool {
	if rc, ok := roles[a.Role]; ok {
		role := &domain.Role{Kind: domain.RoleKind(rc.Kind)}
		if domain.ResolveRoleKind(role, a.Role) == domain.RoleKindInteractive {
			return false
		}
		return a.shouldSuperviseByDesiredState()
	}
	return a.ShouldSupervise()
}

func (a AgentEntry) shouldSuperviseByDesiredState() bool {
	switch a.DesiredState {
	case domain.AgentDesiredStopped, domain.AgentDesiredDraining:
		return false
	default:
		return true
	}
}

// DaemonConfig is the merged, resolved configuration used by callers.
type DaemonConfig struct {
	Backend string                `yaml:"backend,omitempty"`
	Daemon  DaemonSettings        `yaml:"daemon,omitempty"`
	Roles   map[string]RoleConfig `yaml:"roles,omitempty"`
	Agents  []AgentEntry          `yaml:"agents,omitempty"`
}

// LoadDaemonConfig returns the explicit workspace daemon configuration from
// FleetDB. If no workspace is set, it returns built-in defaults so first-run
// commands can still render help and diagnostics.
func LoadDaemonConfig(projectDir string) (*DaemonConfig, error) {
	ctx := cmdstore.RootContext()
	dc := newDefaultDaemonConfig()
	key, err := bootstrap.ResolveActiveWorkspaceKey(ctx, nil)
	if err != nil {
		if errors.Is(err, bootstrap.ErrNoActiveWorkspace) {
			return dc, nil
		}
		return nil, fmt.Errorf("resolve active workspace: %w", err)
	}
	if cached, cacheErr, ok := lookupPrimedDaemonConfig(key, projectDir); ok {
		return cached, cacheErr
	}
	dataDir := bootstrap.LoomDir()
	if dataDir == "" {
		return nil, errors.New("cannot determine loom data directory")
	}
	handle, err := bootstrap.OpenStore(ctx, dataDir, nil)
	if err != nil {
		return nil, fmt.Errorf("open fleet-db store: %w", err)
	}
	// Apply store-level tracing (this path bypasses cmdstore.OpenStore).
	handle.Store = cmdstore.WrapStoreWithTracing(handle.Store)
	defer func() { _ = handle.Close() }()

	return loadDaemonConfigFromStore(ctx, handle.Store, key, dc, projectDir)
}

// newDefaultDaemonConfig returns a DaemonConfig with sensible defaults.
func newDefaultDaemonConfig() *DaemonConfig {
	return &DaemonConfig{
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
}

func loadDaemonConfigFromStore(ctx context.Context, st store.Store, wsKey string, dc *DaemonConfig, projectDir string) (*DaemonConfig, error) {
	if dc == nil {
		dc = newDefaultDaemonConfig()
	}
	profile, err := st.Daemon().Get(ctx, wsKey)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("get daemon profile: %w", err)
	}
	if profile != nil {
		OverlayDaemonSettings(&dc.Daemon, daemonSettingsFromDomain(profile))
		if profile.AgentBackend != "" {
			dc.Backend = profile.AgentBackend
		}
	}
	if dc.Daemon.IssueBackend == "" {
		dc.Daemon.IssueBackend = "fleetdb"
	}

	roles, err := st.Roles().List(ctx, wsKey)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	for _, role := range roles {
		if role == nil {
			continue
		}
		dc.Roles[role.Name] = roleConfigFromDomain(role)
	}

	agents, err := st.Agents().List(ctx, wsKey)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	dc.Agents = make([]AgentEntry, 0, len(agents))
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		dc.Agents = append(dc.Agents, agentEntryFromDomain(agent))
	}

	if err := validateAgents(dc.Agents, dc.Daemon.MaxAgents, dc.Roles); err != nil {
		return nil, err
	}
	if err := ValidateAgentRepos(dc.Agents); err != nil {
		return nil, err
	}
	_ = projectDir
	return dc, nil
}

func daemonSettingsFromDomain(p *domain.DaemonProfile) *DaemonSettings {
	if p == nil {
		return nil
	}
	return &DaemonSettings{
		PIDFile:        p.PIDFile,
		LogDir:         p.LogDir,
		EventsDir:      p.EventsDir,
		RestartPolicy:  restartPolicyFromDomain(p.RestartPolicy),
		MaxAgents:      cloneIntPtr(p.MaxAgents),
		IssueBackend:   p.IssueBackend,
		StartupTimeout: cloneIntPtr(p.StartupTimeout),
		OTel:           otelFromDomain(p.OTel),
	}
}

func restartPolicyFromDomain(r domain.RestartPolicy) RestartPolicy {
	return RestartPolicy{
		MaxRetries:       cloneIntPtr(r.MaxRetries),
		BackoffInitial:   cloneIntPtr(r.BackoffInitial),
		BackoffMax:       cloneIntPtr(r.BackoffMax),
		OutputTimeout:    cloneIntPtr(r.OutputTimeout),
		RateLimitBackoff: cloneIntPtr(r.RateLimitBackoff),
		RateLimitMaxWait: cloneIntPtr(r.RateLimitMaxWait),
		RateLimitNoCount: cloneBoolPtr(r.RateLimitNoCount),
		TimeoutBackoff:   cloneIntPtr(r.TimeoutBackoff),
		NoWorkBackoff:    cloneIntPtr(r.NoWorkBackoff),
		IdlePollInterval: cloneIntPtr(r.IdlePollInterval),
		YieldTimeout:     cloneIntPtr(r.YieldTimeout),
		SigtermTimeout:   cloneIntPtr(r.SigtermTimeout),
	}
}

func roleConfigFromDomain(r *domain.Role) RoleConfig {
	if r == nil {
		return RoleConfig{}
	}
	return RoleConfig{
		Kind:           string(r.Kind),
		Description:    r.Description,
		Prompt:         r.Prompt,
		PromptFile:     r.PromptFile,
		Model:          r.Model,
		TaskFilter:     r.TaskFilter,
		Backend:        r.Backend,
		Effort:         r.Effort,
		PathPatterns:   append([]string(nil), r.PathPatterns...),
		Skills:         append([]string(nil), r.Skills...),
		InputPolicy:    r.InputPolicy.Clone(),
		MaxPriority:    cloneIntPtr(r.MaxPriority),
		MaxConcurrency: cloneIntPtr(r.MaxConcurrency),
		ReadOnly:       r.ReadOnly,
		AllowedTools:   append([]string(nil), r.AllowedTools...),
		DeniedTools:    append([]string(nil), r.DeniedTools...),
		MaxBudgetUSD:   cloneFloatPtr(r.MaxBudgetUSD),
		MaxRunDuration: cloneIntPtr(r.MaxRunDuration),
	}
}

func agentEntryFromDomain(a *domain.Agent) AgentEntry {
	if a == nil {
		return AgentEntry{}
	}
	return AgentEntry{
		Worktree:         a.Name,
		Role:             a.RoleName,
		Auto:             a.Auto,
		Backend:          a.Backend,
		FallbackBackends: append([]string(nil), a.FallbackBackends...),
		Repos:            append([]string(nil), a.Repos...),
		RepoGroups:       append([]string(nil), a.RepoGroups...),
		CrossRepo:        a.CrossRepo,
		Parent:           a.Parent,
		Mode:             a.Mode,
		DesiredState:     a.DesiredState,
		Hooks:            a.Hooks.Clone(),
	}
}

func otelFromDomain(o *domain.OTelSettings) *OTelDaemonConfig {
	if o == nil {
		return nil
	}
	return &OTelDaemonConfig{
		Enabled:         o.Enabled,
		Endpoint:        o.Endpoint,
		Protocol:        o.Protocol,
		ServiceName:     o.ServiceName,
		SampleRate:      o.SampleRate,
		FlushIntervalMs: o.FlushIntervalMs,
		Traces:          cloneBoolPtr(o.Traces),
		Metrics:         cloneBoolPtr(o.Metrics),
	}
}

func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func cloneFloatPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

// validateAgents checks that agent entries and max_agents limits are valid.
func validateAgents(agents []AgentEntry, maxAgents *int, roles map[string]RoleConfig) error {
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
	runnable := 0
	for _, a := range agents {
		if a.ShouldSuperviseWithRoles(roles) {
			runnable++
		}
	}
	if maxAgents != nil && *maxAgents > 0 && runnable > *maxAgents {
		return fmt.Errorf("too many runnable agents configured: %d exceeds max_agents limit of %d", runnable, *maxAgents)
	}
	return nil
}

// ValidateAgentRepos checks that agent Repo fields reference valid repos in the workspace config.
// In workspace mode, unknown repo names are hard errors. Outside workspace mode, Repo fields
// trigger a warning but are not blocking.
func ValidateAgentRepos(agents []AgentEntry) error {
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

// OverlayDaemonSettings applies explicitly-set values from src onto dst.
func OverlayDaemonSettings(dst *DaemonSettings, src *DaemonSettings) {
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
	if src.IssueBackend != "" {
		dst.IssueBackend = src.IssueBackend
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
