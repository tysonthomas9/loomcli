package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// GitRunner wraps git command execution.
type GitRunner interface {
	// Run executes a git command and captures output.
	Run(dir string, args ...string) CommandResult
	// RunWithOutput executes a git command, streaming to stdout/stderr.
	RunWithOutput(dir string, args ...string) error
}

// ContextGitRunner is implemented by GitRunner values that can terminate an
// in-flight git subprocess when the caller's context is canceled. Callers
// with a hard deadline should require this interface instead of wrapping
// GitRunner.Run in a goroutine, which would leave the subprocess running.
type ContextGitRunner interface {
	RunContext(ctx context.Context, dir string, args ...string) CommandResult
}

// ExecRunner wraps arbitrary command execution.
type ExecRunner interface {
	Run(dir, name string, args ...string) CommandResult
}

// ExecContextRunner wraps context-aware command execution.
type ExecContextRunner interface {
	Run(ctx context.Context, dir, name string, args ...string) CommandResult
}

// AgentInvoker wraps agent invocation (interactive and non-interactive).
type AgentInvoker interface {
	InvokeInteractive(workDir, prompt, agentName string) error
	InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error
}

// TextAnalyzer wraps one-shot text analysis. Callers choose the concrete
// backend; tests can inject deterministic analyzers without shelling out.
type TextAnalyzer interface {
	AnalyzeText(ctx context.Context, workDir, prompt string) (string, error)
}

type TextAnalyzerFunc func(ctx context.Context, workDir, prompt string) (string, error)

func (f TextAnalyzerFunc) AnalyzeText(ctx context.Context, workDir, prompt string) (string, error) {
	return f(ctx, workDir, prompt)
}

// FileSystem wraps file operations.
type FileSystem interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	Stat(path string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	Remove(path string) error
}

// Deps is the central dependency-injection container for CLI commands.
// It holds all external dependencies so they can be swapped for testing.
type Deps struct {
	Git          GitRunner
	Exec         ExecRunner
	FS           FileSystem
	Logger       *slog.Logger
	Clock        func() time.Time
	IssueBackend backend.IssueBackend
	LookPath     func(file string) (string, error)
	ExecCtx      ExecContextRunner
	Agent        AgentInvoker
	TextAnalyzer TextAnalyzer
}

// --- default implementations ---

type defaultGitRunner struct{}

func (defaultGitRunner) Run(dir string, args ...string) CommandResult {
	return ensureDefaultDeps().Exec.Run(dir, "git", args...)
}

func (defaultGitRunner) RunContext(ctx context.Context, dir string, args ...string) CommandResult {
	return ensureDefaultDeps().ExecCtx.Run(ctx, dir, "git", args...)
}

func (defaultGitRunner) RunWithOutput(dir string, args ...string) error {
	return defaultRunGitWithOutput(dir, args...)
}

type defaultExecRunner struct{}

func (defaultExecRunner) Run(dir, name string, args ...string) CommandResult {
	return defaultExecCommand(dir, name, args...)
}

type defaultFileSystem struct{}

func (defaultFileSystem) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path) //nolint:gosec // G304: thin wrapper; callers control path
}

func (defaultFileSystem) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func (defaultFileSystem) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func (defaultFileSystem) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (defaultFileSystem) Remove(path string) error {
	return os.Remove(path)
}

// registryAgentInvoker delegates to InvokeAgent/InvokeAgentNonInteractive (backend registry).
type registryAgentInvoker struct{}

func (registryAgentInvoker) InvokeInteractive(workDir, prompt, agentName string) error {
	return InvokeAgent(workDir, prompt, agentName)
}

func (registryAgentInvoker) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	return InvokeAgentNonInteractive(workDir, prompt, agentName, shutdown, collector)
}

type defaultExecContextRunner struct{}

func (defaultExecContextRunner) Run(ctx context.Context, dir, name string, args ...string) CommandResult {
	return defaultExecCommandContext(ctx, dir, name, args...)
}

// DefaultDeps returns a Deps populated with real (production) implementations.
func DefaultDeps(ctx context.Context) *Deps {
	var issueBackend backend.IssueBackend

	switch ResolveIssueBackendType() {
	case IssueBackendFleetDB:
		issueBackend = newFleetDBIssueBackend()
	case IssueBackendFleet:
		fb, err := createFleetIssueBackend()
		if err != nil {
			slog.Error("fleet backend creation failed", "err", err)
			issueBackend = newUnavailableIssueBackend(IssueBackendFleet, err)
		} else {
			issueBackend = fb
		}
	case IssueBackendAPI:
		ab, err := createAPIIssueBackend()
		if err != nil {
			slog.Error("api backend creation failed", "err", err)
			issueBackend = newUnavailableIssueBackend(IssueBackendAPI, err)
		} else {
			issueBackend = ab
		}
	}
	if issueBackend == nil {
		issueBackend = newFleetDBIssueBackend()
	}

	// Wrap the resolved IssueBackend with tracing so every method call
	// emits a `service.IssueBackend.<Method>` sub-span under the active
	// CLI / HTTP-server / agent span. Applied after backend selection so
	// fleet-db, fleet, and api backends all get the same instrumentation.
	// See issue_backend_tracing.go.
	issueBackend = wrapIssueBackendWithTracing(issueBackend)

	return &Deps{
		// Wrap the default git runner with tracing so every git subprocess
		// (push/pull/fetch/merge/status/etc.) emits a sub-span under the
		// active loom.cli span. See git_runner_tracing.go.
		Git:          wrapGitRunnerWithTracing(ctx, defaultGitRunner{}),
		Exec:         defaultExecRunner{},
		FS:           defaultFileSystem{},
		Logger:       slog.Default(),
		Clock:        time.Now,
		IssueBackend: issueBackend,
		LookPath:     exec.LookPath,
		ExecCtx:      defaultExecContextRunner{},
		// Wrap the registry-backed invoker with tracing so every backend call
		// from agent flows (plan/task/automode/etc.) emits a sub-span under
		// the active loom.cli span. See agent_invoker_tracing.go.
		Agent: wrapAgentInvokerWithTracing(ctx, registryAgentInvoker{}),
	}
}

// defaultDeps is the package-level Deps instance used by package helpers.
// Built lazily on first access (see ensureDefaultDeps) and refreshed in
// root.PersistentPreRunE after the --workspace / --server flag-to-env
// mirror runs, so the cached fleet client reflects the actual workspace
// instead of whatever the shell exported at process start.
//
// Tests assign directly into this var; production code must go through
// ensureDefaultDeps() to avoid a nil deref before the first command runs.
var defaultDeps *Deps

// ensureDefaultDeps lazily builds the package-level Deps. Returns the
// current value unchanged if already set (by PersistentPreRunE or by a
// test override).
func ensureDefaultDeps() *Deps {
	if defaultDeps == nil {
		defaultDeps = DefaultDeps(context.Background())
	}
	return defaultDeps
}

// --- cobra context helpers ---

type depsKeyType struct{}

var depsKey = depsKeyType{}

// WithDeps stores a *Deps in the given context.
func WithDeps(ctx context.Context, d *Deps) context.Context {
	return context.WithValue(ctx, depsKey, d)
}

// GetDeps extracts the *Deps from a cobra command's context.
// If none is set, it returns the lazily-built default singleton so that
// test-time swaps via installExecMock/etc. are visible to all callers.
func GetDeps(cmd *cobra.Command) *Deps {
	if cmd == nil {
		return ensureDefaultDeps()
	}
	ctx := cmd.Context()
	if ctx == nil {
		return ensureDefaultDeps()
	}
	if d, ok := ctx.Value(depsKey).(*Deps); ok && d != nil {
		return d
	}
	return ensureDefaultDeps()
}

// TestingGetDefaultDeps returns the package-level defaultDeps for use by test
// packages that need to swap global state. Production code should use GetDeps.
func TestingGetDefaultDeps() *Deps {
	return ensureDefaultDeps()
}

// fleetDBIssueBackend lazily opens fleet-db for IssueBackend calls. It avoids
// spawning embedded fleet-db during package init while still making fleet-db
// the default CLI issue backend.
type fleetDBIssueBackend struct{}

var _ backend.IssueBackend = (*fleetDBIssueBackend)(nil)
var _ backend.ClaimReleaser = (*fleetDBIssueBackend)(nil)
var _ workitems.ReadyQueries = (*fleetDBIssueBackend)(nil)
var _ workitems.BlockedQueries = (*fleetDBIssueBackend)(nil)
var _ workitems.StatsQueries = (*fleetDBIssueBackend)(nil)
var _ workitems.EventQueries = (*fleetDBIssueBackend)(nil)
var _ workitems.CommentQueries = (*fleetDBIssueBackend)(nil)
var _ workitems.CommentCommands = (*fleetDBIssueBackend)(nil)
var _ workitems.DependencyCommands = (*fleetDBIssueBackend)(nil)

func newFleetDBIssueBackend() backend.IssueBackend {
	return &fleetDBIssueBackend{}
}

func (b *fleetDBIssueBackend) withBackend(ctx context.Context, op string, fn func(backend.IssueBackend) error) error {
	dataDir := bootstrap.LoomDir()
	if dataDir == "" {
		return backend.ErrUnavailable(op, "cannot resolve loom data directory; set HOME or LOOM_CONFIG_DIR", nil)
	}
	handle, err := bootstrap.OpenStore(ctx, dataDir, nil)
	if err != nil {
		return backend.ErrUnavailable(op, "open fleet-db store", err)
	}
	defer func() { _ = handle.Close() }()

	ws, err := bootstrap.ResolveActiveWorkspaceKey(ctx, handle.Store.Workspaces())
	if err != nil {
		return backend.ErrUnavailable(op, "resolve active fleet-db workspace", err)
	}
	fb, err := fleet.New(fleet.Config{
		BaseURL:     handle.URL(),
		WorkspaceID: ws,
		APIKey:      handle.FleetDBClientAPIKey(),
		Actor:       fleetDBActor(),
	})
	if err != nil {
		return backend.ErrUnavailable(op, "create fleet-db issue backend", err)
	}
	return fn(fb)
}

func fleetDBActor() string {
	if v := os.Getenv(bootstrap.EnvFleetDBActor); v != "" {
		return v
	}
	if v := os.Getenv(bootstrap.EnvAgentName); v != "" {
		return v
	}
	return os.Getenv("USER")
}

func (b *fleetDBIssueBackend) Get(ctx context.Context, id string) (*backend.IssueDetailData, error) {
	var out *backend.IssueDetailData
	err := b.withBackend(ctx, "Get", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.Get(ctx, id)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	var out []backend.IssueData
	err := b.withBackend(ctx, "List", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.List(ctx, opts)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) Ready(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	var out []workitems.IssueSummary
	err := b.withBackend(ctx, "Ready", func(ib backend.IssueBackend) error {
		ready, ok := ib.(workitems.ReadyQueries)
		if !ok {
			return workitems.ErrUnavailable
		}
		var err error
		out, err = ready.Ready(ctx, query)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) Blocked(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	var out []workitems.IssueSummary
	err := b.withBackend(ctx, "Blocked", func(ib backend.IssueBackend) error {
		blocked, ok := ib.(workitems.BlockedQueries)
		if !ok {
			return workitems.ErrUnavailable
		}
		var err error
		out, err = blocked.Blocked(ctx, query)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) Stats(ctx context.Context) (*workitems.Stats, error) {
	var out *workitems.Stats
	err := b.withBackend(ctx, "Stats", func(ib backend.IssueBackend) error {
		stats, ok := ib.(workitems.StatsQueries)
		if !ok {
			return workitems.ErrUnavailable
		}
		var err error
		out, err = stats.Stats(ctx)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) Search(ctx context.Context, query workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	var out []workitems.IssueSummary
	err := b.withBackend(ctx, "Search", func(ib backend.IssueBackend) error {
		search, ok := ib.(workitems.SearchQueries)
		if !ok {
			return workitems.ErrUnavailable
		}
		var err error
		out, err = search.Search(ctx, query)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	var out *backend.IssueData
	err := b.withBackend(ctx, "Create", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.Create(ctx, params)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) Update(ctx context.Context, id string, params backend.UpdateParams) error {
	return b.withBackend(ctx, "Update", func(ib backend.IssueBackend) error {
		return ib.Update(ctx, id, params)
	})
}

func (b *fleetDBIssueBackend) ClaimIssue(ctx context.Context, id string, lockTTL time.Duration) error {
	return b.withBackend(ctx, "ClaimIssue", func(ib backend.IssueBackend) error {
		return ib.ClaimIssue(ctx, id, lockTTL)
	})
}

// ReleaseClaim implements backend.ClaimReleaser by forwarding to the
// underlying FleetBackend (which is the only IssueBackend implementation
// that maintains an explicit claim lock distinct from issue status).
// Used by `loom complete` to close the planner-leaked-lock path in LOOM-1.
func (b *fleetDBIssueBackend) ReleaseClaim(ctx context.Context, id, actor string) error {
	return b.withBackend(ctx, "ReleaseClaim", func(ib backend.IssueBackend) error {
		r, ok := ib.(backend.ClaimReleaser)
		if !ok {
			return nil
		}
		return r.ReleaseClaim(ctx, id, actor)
	})
}

func (b *fleetDBIssueBackend) ClaimIssueAsActor(ctx context.Context, id string, lockTTL time.Duration, actor string) error {
	return b.withBackend(ctx, "ClaimIssue", func(ib backend.IssueBackend) error {
		if actorBackend, ok := ib.(interface {
			ClaimIssueAsActor(context.Context, string, time.Duration, string) error
		}); ok {
			return actorBackend.ClaimIssueAsActor(ctx, id, lockTTL, actor)
		}
		return ib.ClaimIssue(ctx, id, lockTTL)
	})
}

func (b *fleetDBIssueBackend) RenewIssueClaimAsActor(ctx context.Context, id string, lockTTL time.Duration, actor string) error {
	return b.withBackend(ctx, "RenewIssueClaim", func(ib backend.IssueBackend) error {
		if actorBackend, ok := ib.(interface {
			RenewIssueClaimAsActor(context.Context, string, time.Duration, string) error
		}); ok {
			return actorBackend.RenewIssueClaimAsActor(ctx, id, lockTTL, actor)
		}
		return fmt.Errorf("renew issue claim: backend does not support renewal-only claims")
	})
}

func (b *fleetDBIssueBackend) ReleaseIssueLock(ctx context.Context, id, actor string) error {
	return b.withBackend(ctx, "ReleaseIssueLock", func(ib backend.IssueBackend) error {
		return ib.ReleaseIssueLock(ctx, id, actor)
	})
}

// ReleaseIssueAsActor is the actor-scoped release used by the supervisor to
// free a lock acquired via ClaimIssueAsActor when the agent process exits.
// Falls back to ReleaseIssueLock(id, actor) if the underlying backend does
// not expose a dedicated actor variant.
func (b *fleetDBIssueBackend) ReleaseIssueAsActor(ctx context.Context, id string, actor string) error {
	return b.withBackend(ctx, "ReleaseIssue", func(ib backend.IssueBackend) error {
		if actorBackend, ok := ib.(interface {
			ReleaseIssueAsActor(context.Context, string, string) error
		}); ok {
			return actorBackend.ReleaseIssueAsActor(ctx, id, actor)
		}
		return ib.ReleaseIssueLock(ctx, id, actor)
	})
}

func (b *fleetDBIssueBackend) Close(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	var out *backend.CloseResult
	err := b.withBackend(ctx, "Close", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.Close(ctx, id, params)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) Reopen(ctx context.Context, id string, params backend.ReopenParams) error {
	return b.withBackend(ctx, "Reopen", func(ib backend.IssueBackend) error {
		return ib.Reopen(ctx, id, params)
	})
}

func (b *fleetDBIssueBackend) Delete(ctx context.Context, params backend.DeleteParams) error {
	return b.withBackend(ctx, "Delete", func(ib backend.IssueBackend) error {
		return ib.Delete(ctx, params)
	})
}

func (b *fleetDBIssueBackend) AddDependency(ctx context.Context, command workitems.AddDependencyCommand) error {
	return b.withBackend(ctx, "AddDependency", func(ib backend.IssueBackend) error {
		dependencies, ok := ib.(workitems.DependencyCommands)
		if !ok {
			return backend.ErrUnavailable("AddDependency", "work items dependency commands unavailable", nil)
		}
		return dependencies.AddDependency(ctx, command)
	})
}

func (b *fleetDBIssueBackend) RemoveDependency(ctx context.Context, command workitems.RemoveDependencyCommand) error {
	return b.withBackend(ctx, "RemoveDependency", func(ib backend.IssueBackend) error {
		dependencies, ok := ib.(workitems.DependencyCommands)
		if !ok {
			return backend.ErrUnavailable("RemoveDependency", "work items dependency commands unavailable", nil)
		}
		return dependencies.RemoveDependency(ctx, command)
	})
}

func (b *fleetDBIssueBackend) ListComments(ctx context.Context, query workitems.ListCommentsQuery) ([]*workitems.Comment, error) {
	var out []*workitems.Comment
	err := b.withBackend(ctx, "ListComments", func(ib backend.IssueBackend) error {
		comments, ok := ib.(workitems.CommentQueries)
		if !ok {
			return backend.ErrUnavailable("ListComments", "work items comment queries unavailable", nil)
		}
		var err error
		out, err = comments.ListComments(ctx, query)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) AddComment(ctx context.Context, command workitems.AddCommentCommand) (*workitems.Comment, error) {
	var out *workitems.Comment
	err := b.withBackend(ctx, "AddComment", func(ib backend.IssueBackend) error {
		comments, ok := ib.(workitems.CommentCommands)
		if !ok {
			return backend.ErrUnavailable("AddComment", "work items comment commands unavailable", nil)
		}
		var err error
		out, err = comments.AddComment(ctx, command)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) ListEvents(ctx context.Context, query workitems.ListEventsQuery) ([]*workitems.Event, error) {
	var out []*workitems.Event
	err := b.withBackend(ctx, "ListEvents", func(ib backend.IssueBackend) error {
		events, ok := ib.(workitems.EventQueries)
		if !ok {
			return backend.ErrUnavailable("ListEvents", "work items event queries unavailable", nil)
		}
		var err error
		out, err = events.ListEvents(ctx, query)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) BackendName() string { return "fleet-db" }

type unavailableIssueBackend struct {
	name string
	err  error
}

var _ backend.IssueBackend = (*unavailableIssueBackend)(nil)
var _ workitems.ReadyQueries = (*unavailableIssueBackend)(nil)
var _ workitems.BlockedQueries = (*unavailableIssueBackend)(nil)
var _ workitems.StatsQueries = (*unavailableIssueBackend)(nil)
var _ workitems.EventQueries = (*unavailableIssueBackend)(nil)
var _ workitems.CommentQueries = (*unavailableIssueBackend)(nil)
var _ workitems.CommentCommands = (*unavailableIssueBackend)(nil)
var _ workitems.DependencyCommands = (*unavailableIssueBackend)(nil)

func newUnavailableIssueBackend(name string, err error) backend.IssueBackend {
	return &unavailableIssueBackend{name: name, err: err}
}

func (b *unavailableIssueBackend) unavailable(op string) error {
	return backend.ErrUnavailable(op, fmt.Sprintf("%s issue backend unavailable", b.name), b.err)
}

func (b *unavailableIssueBackend) Get(context.Context, string) (*backend.IssueDetailData, error) {
	return nil, b.unavailable("Get")
}
func (b *unavailableIssueBackend) List(context.Context, backend.ListOpts) ([]backend.IssueData, error) {
	return nil, b.unavailable("List")
}
func (b *unavailableIssueBackend) Ready(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	return nil, b.unavailable("Ready")
}
func (b *unavailableIssueBackend) Blocked(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	return nil, b.unavailable("Blocked")
}
func (b *unavailableIssueBackend) Stats(context.Context) (*workitems.Stats, error) {
	return nil, b.unavailable("Stats")
}
func (b *unavailableIssueBackend) Search(context.Context, workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	return nil, b.unavailable("Search")
}
func (b *unavailableIssueBackend) Create(context.Context, backend.CreateParams) (*backend.IssueData, error) {
	return nil, b.unavailable("Create")
}
func (b *unavailableIssueBackend) Update(context.Context, string, backend.UpdateParams) error {
	return b.unavailable("Update")
}
func (b *unavailableIssueBackend) ClaimIssue(context.Context, string, time.Duration) error {
	return b.unavailable("ClaimIssue")
}
func (b *unavailableIssueBackend) ReleaseIssueLock(context.Context, string, string) error {
	return b.unavailable("ReleaseIssueLock")
}
func (b *unavailableIssueBackend) Close(context.Context, string, backend.CloseParams) (*backend.CloseResult, error) {
	return nil, b.unavailable("Close")
}
func (b *unavailableIssueBackend) Reopen(context.Context, string, backend.ReopenParams) error {
	return b.unavailable("Reopen")
}
func (b *unavailableIssueBackend) Delete(context.Context, backend.DeleteParams) error {
	return b.unavailable("Delete")
}
func (b *unavailableIssueBackend) AddDependency(context.Context, workitems.AddDependencyCommand) error {
	return b.unavailable("AddDependency")
}
func (b *unavailableIssueBackend) RemoveDependency(context.Context, workitems.RemoveDependencyCommand) error {
	return b.unavailable("RemoveDependency")
}
func (b *unavailableIssueBackend) ListComments(context.Context, workitems.ListCommentsQuery) ([]*workitems.Comment, error) {
	return nil, b.unavailable("ListComments")
}
func (b *unavailableIssueBackend) AddComment(context.Context, workitems.AddCommentCommand) (*workitems.Comment, error) {
	return nil, b.unavailable("AddComment")
}
func (b *unavailableIssueBackend) ListEvents(context.Context, workitems.ListEventsQuery) ([]*workitems.Event, error) {
	return nil, b.unavailable("ListEvents")
}
func (b *unavailableIssueBackend) BackendName() string { return b.name + "-unavailable" }
