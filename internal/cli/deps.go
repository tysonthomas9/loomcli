package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

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
	WorkItems    workitems.API
	ClaimLeases  workitems.ClaimLeaseCommands
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
	var workItemAPI workitems.API
	var claimLeases workitems.ClaimLeaseCommands

	switch ResolveWorkItemsAdapterType() {
	case WorkItemsAdapterFleetDB:
		store := newFleetDBWorkItemsAdapter()
		workItemAPI, _ = workitems.New(store)
		claimLeases = store
	case WorkItemsAdapterFleet:
		fb, err := createFleetWorkItemStore()
		if err != nil {
			slog.Error("fleet backend creation failed", "err", err)
			workItemAPI = newUnavailableWorkItems(WorkItemsAdapterFleet, err)
		} else {
			workItemAPI, _ = workitems.New(fb)
			claimLeases = fb
		}
	case WorkItemsAdapterAPI:
		ab, err := createAPIWorkItems()
		if err != nil {
			slog.Error("api backend creation failed", "err", err)
			workItemAPI = newUnavailableWorkItems(WorkItemsAdapterAPI, err)
		} else {
			workItemAPI = ab
		}
	}
	if workItemAPI == nil {
		store := newFleetDBWorkItemsAdapter()
		workItemAPI, _ = workitems.New(store)
		claimLeases = store
	}
	workItemAPI = wrapWorkItemsWithTracing(workItemAPI)

	return &Deps{
		// Wrap the default git runner with tracing so every git subprocess
		// (push/pull/fetch/merge/status/etc.) emits a sub-span under the
		// active loom.cli span. See git_runner_tracing.go.
		Git:         wrapGitRunnerWithTracing(ctx, defaultGitRunner{}),
		Exec:        defaultExecRunner{},
		FS:          defaultFileSystem{},
		Logger:      slog.Default(),
		Clock:       time.Now,
		WorkItems:   workItemAPI,
		ClaimLeases: claimLeases,
		LookPath:    exec.LookPath,
		ExecCtx:     defaultExecContextRunner{},
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

// fleetDBWorkItemsAdapter lazily opens FleetDB for Work Items calls. It avoids
// spawning embedded fleet-db during package init while still making fleet-db
// the default CLI Work Items adapter.
type fleetDBWorkItemsAdapter struct{}

var _ workitems.Store = (*fleetDBWorkItemsAdapter)(nil)
var _ workitems.ClaimLeaseCommands = (*fleetDBWorkItemsAdapter)(nil)

func newFleetDBWorkItemsAdapter() *fleetDBWorkItemsAdapter {
	return &fleetDBWorkItemsAdapter{}
}

func (b *fleetDBWorkItemsAdapter) withStore(ctx context.Context, op string, fn func(*fleet.FleetBackend) error) error {
	dataDir := bootstrap.LoomDir()
	if dataDir == "" {
		return workitems.AdapterUnavailable(op, "cannot resolve loom data directory; set HOME or LOOM_CONFIG_DIR", nil)
	}
	handle, err := bootstrap.OpenStore(ctx, dataDir, nil)
	if err != nil {
		return workitems.AdapterUnavailable(op, "open fleet-db store", err)
	}
	defer func() { _ = handle.Close() }()

	ws, err := bootstrap.ResolveActiveWorkspaceKey(ctx, handle.Store.Workspaces())
	if err != nil {
		return workitems.AdapterUnavailable(op, "resolve active fleet-db workspace", err)
	}
	fb, err := fleet.New(fleet.Config{
		BaseURL:     handle.URL(),
		WorkspaceID: ws,
		APIKey:      handle.FleetDBClientAPIKey(),
		Actor:       fleetDBActor(),
	})
	if err != nil {
		return workitems.AdapterUnavailable(op, "create FleetDB Work Items adapter", err)
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

func (b *fleetDBWorkItemsAdapter) RequireRepositoryAdmission(ctx context.Context) error {
	return b.withStore(ctx, "RequireRepositoryAdmission", func(store *fleet.FleetBackend) error {
		return store.RequireRepositoryAdmission(ctx)
	})
}

func (b *fleetDBWorkItemsAdapter) Get(ctx context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
	var out *workitems.IssueDetail
	err := b.withStore(ctx, "Get", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.Get(ctx, query)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) List(ctx context.Context, opts workitems.ListFilter) ([]workitems.IssueSummary, error) {
	var out []workitems.IssueSummary
	err := b.withStore(ctx, "List", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.List(ctx, opts)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) Ready(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	var out []workitems.IssueSummary
	err := b.withStore(ctx, "Ready", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.Ready(ctx, query)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) Blocked(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	var out []workitems.IssueSummary
	err := b.withStore(ctx, "Blocked", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.Blocked(ctx, query)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) Deferred(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	var out []workitems.IssueSummary
	err := b.withStore(ctx, "Deferred", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.Deferred(ctx, query)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) Stats(ctx context.Context) (*workitems.Stats, error) {
	var out *workitems.Stats
	err := b.withStore(ctx, "Stats", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.Stats(ctx)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) Search(ctx context.Context, query workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	var out []workitems.IssueSummary
	err := b.withStore(ctx, "Search", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.Search(ctx, query)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) Create(ctx context.Context, command workitems.CreateCommand) (*workitems.IssueSummary, error) {
	var out *workitems.IssueSummary
	err := b.withStore(ctx, "Create", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.Create(ctx, command)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) Patch(ctx context.Context, command workitems.PatchCommand) error {
	return b.withStore(ctx, "Patch", func(store *fleet.FleetBackend) error {
		return store.Patch(ctx, command)
	})
}

func (b *fleetDBWorkItemsAdapter) Claim(ctx context.Context, command workitems.ClaimCommand) (*workitems.IssueDetail, error) {
	var out *workitems.IssueDetail
	err := b.withStore(ctx, "Claim", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.Claim(ctx, command)
		return err
	})
	return out, err
}

// ReleaseClaim implements backend.ClaimReleaser by forwarding to the
// underlying Fleet adapter (the only Work Items adapter
// that maintains an explicit claim lock distinct from issue status).
// Used by `loom complete` to close the planner-leaked-lock path in LOOM-1.
func (b *fleetDBWorkItemsAdapter) ReleaseClaim(ctx context.Context, id, actor string) error {
	return b.withStore(ctx, "ReleaseClaim", func(store *fleet.FleetBackend) error {
		return store.ReleaseClaim(ctx, id, actor)
	})
}

func (b *fleetDBWorkItemsAdapter) ClaimAsActor(ctx context.Context, id string, lockTTL time.Duration, actor string) error {
	return b.withStore(ctx, "Claim", func(store *fleet.FleetBackend) error {
		return store.ClaimAsActor(ctx, id, lockTTL, actor)
	})
}

func (b *fleetDBWorkItemsAdapter) RenewClaimAsActor(ctx context.Context, id string, lockTTL time.Duration, actor string) error {
	return b.withStore(ctx, "RenewClaim", func(store *fleet.FleetBackend) error {
		return store.RenewClaimAsActor(ctx, id, lockTTL, actor)
	})
}

func (b *fleetDBWorkItemsAdapter) ReleaseIssueLock(ctx context.Context, id, actor string) error {
	return b.withStore(ctx, "ReleaseIssueLock", func(store *fleet.FleetBackend) error {
		return store.ReleaseIssueLock(ctx, id, actor)
	})
}

// ReleaseIssueAsActor is the actor-scoped release used by the supervisor to
// free a lock acquired via ClaimIssueAsActor when the agent process exits.
// Falls back to ReleaseIssueLock(id, actor) if the underlying backend does
// not expose a dedicated actor variant.
func (b *fleetDBWorkItemsAdapter) ReleaseIssueAsActor(ctx context.Context, id string, actor string) error {
	return b.withStore(ctx, "ReleaseIssue", func(store *fleet.FleetBackend) error {
		return store.ReleaseIssueAsActor(ctx, id, actor)
	})
}

func (b *fleetDBWorkItemsAdapter) Close(ctx context.Context, command workitems.CloseCommand) (*workitems.CloseResult, error) {
	var out *workitems.CloseResult
	err := b.withStore(ctx, "Close", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.Close(ctx, command)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) Reopen(ctx context.Context, command workitems.ReopenCommand) error {
	return b.withStore(ctx, "Reopen", func(store *fleet.FleetBackend) error {
		return store.Reopen(ctx, command)
	})
}

func (b *fleetDBWorkItemsAdapter) Delete(ctx context.Context, command workitems.DeleteCommand) (workitems.DeleteResult, error) {
	var out workitems.DeleteResult
	err := b.withStore(ctx, "Delete", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.Delete(ctx, command)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) BlockRepositoryRequired(ctx context.Context, id string) (*workitems.RepositoryAdmissionResult, error) {
	var out *workitems.RepositoryAdmissionResult
	err := b.withStore(ctx, "BlockRepositoryRequired", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.BlockRepositoryRequired(ctx, id)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) AssignRepository(ctx context.Context, command workitems.AssignRepositoryCommand) (*workitems.IssueSummary, error) {
	var out *workitems.IssueSummary
	err := b.withStore(ctx, "AssignRepository", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.AssignRepository(ctx, command)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) AddDependency(ctx context.Context, command workitems.AddDependencyCommand) error {
	return b.withStore(ctx, "AddDependency", func(store *fleet.FleetBackend) error {
		return store.AddDependency(ctx, command)
	})
}

func (b *fleetDBWorkItemsAdapter) RemoveDependency(ctx context.Context, command workitems.RemoveDependencyCommand) error {
	return b.withStore(ctx, "RemoveDependency", func(store *fleet.FleetBackend) error {
		return store.RemoveDependency(ctx, command)
	})
}

func (b *fleetDBWorkItemsAdapter) ListDependencies(ctx context.Context, query workitems.ListDependenciesQuery) ([]workitems.Dependency, error) {
	var out []workitems.Dependency
	err := b.withStore(ctx, "ListDependencies", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.ListDependencies(ctx, query)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) ListComments(ctx context.Context, query workitems.ListCommentsQuery) ([]*workitems.Comment, error) {
	var out []*workitems.Comment
	err := b.withStore(ctx, "ListComments", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.ListComments(ctx, query)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) AddComment(ctx context.Context, command workitems.AddCommentCommand) (*workitems.Comment, error) {
	var out *workitems.Comment
	err := b.withStore(ctx, "AddComment", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.AddComment(ctx, command)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) ListEvents(ctx context.Context, query workitems.ListEventsQuery) ([]*workitems.Event, error) {
	var out []*workitems.Event
	err := b.withStore(ctx, "ListEvents", func(store *fleet.FleetBackend) error {
		var err error
		out, err = store.ListEvents(ctx, query)
		return err
	})
	return out, err
}

func (b *fleetDBWorkItemsAdapter) BackendName() string { return "fleet-db" }

type unavailableWorkItems struct {
	name string
	err  error
}

var _ workitems.API = (*unavailableWorkItems)(nil)

func newUnavailableWorkItems(name string, err error) workitems.API {
	return &unavailableWorkItems{name: name, err: err}
}

func (b *unavailableWorkItems) unavailable(op string) error {
	return workitems.AdapterUnavailable(op, fmt.Sprintf("%s Work Items unavailable", b.name), b.err)
}

func (b *unavailableWorkItems) Get(context.Context, workitems.GetQuery) (*workitems.IssueDetail, error) {
	return nil, b.unavailable("Get")
}
func (b *unavailableWorkItems) List(context.Context, workitems.ListQuery) (*workitems.ListResult, error) {
	return nil, b.unavailable("List")
}
func (b *unavailableWorkItems) Ready(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	return nil, b.unavailable("Ready")
}
func (b *unavailableWorkItems) Deferred(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	return nil, b.unavailable("Deferred")
}
func (b *unavailableWorkItems) Blocked(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	return nil, b.unavailable("Blocked")
}
func (b *unavailableWorkItems) Stats(context.Context) (*workitems.Stats, error) {
	return nil, b.unavailable("Stats")
}
func (b *unavailableWorkItems) Search(context.Context, workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	return nil, b.unavailable("Search")
}
func (b *unavailableWorkItems) Create(context.Context, workitems.CreateCommand) (*workitems.IssueSummary, error) {
	return nil, b.unavailable("Create")
}
func (b *unavailableWorkItems) Patch(context.Context, workitems.PatchCommand) (*workitems.IssueDetail, error) {
	return nil, b.unavailable("Patch")
}
func (b *unavailableWorkItems) Claim(context.Context, workitems.ClaimCommand) (*workitems.IssueDetail, error) {
	return nil, b.unavailable("Claim")
}
func (b *unavailableWorkItems) Close(context.Context, workitems.CloseCommand) (*workitems.CloseResult, error) {
	return nil, b.unavailable("Close")
}
func (b *unavailableWorkItems) Reopen(context.Context, workitems.ReopenCommand) error {
	return b.unavailable("Reopen")
}
func (b *unavailableWorkItems) Delete(context.Context, workitems.DeleteCommand) (workitems.DeleteResult, error) {
	return workitems.DeleteResult{}, b.unavailable("Delete")
}
func (b *unavailableWorkItems) BlockRepositoryRequired(context.Context, workitems.BlockRepositoryRequiredCommand) (*workitems.RepositoryAdmissionResult, error) {
	return nil, b.unavailable("BlockRepositoryRequired")
}
func (b *unavailableWorkItems) AssignRepository(context.Context, workitems.AssignRepositoryCommand) (*workitems.IssueSummary, error) {
	return nil, b.unavailable("AssignRepository")
}
func (b *unavailableWorkItems) AddDependency(context.Context, workitems.AddDependencyCommand) error {
	return b.unavailable("AddDependency")
}
func (b *unavailableWorkItems) RemoveDependency(context.Context, workitems.RemoveDependencyCommand) error {
	return b.unavailable("RemoveDependency")
}
func (b *unavailableWorkItems) ListDependencies(context.Context, workitems.ListDependenciesQuery) ([]workitems.Dependency, error) {
	return nil, b.unavailable("ListDependencies")
}
func (b *unavailableWorkItems) ListComments(context.Context, workitems.ListCommentsQuery) ([]*workitems.Comment, error) {
	return nil, b.unavailable("ListComments")
}
func (b *unavailableWorkItems) AddComment(context.Context, workitems.AddCommentCommand) (*workitems.Comment, error) {
	return nil, b.unavailable("AddComment")
}
func (b *unavailableWorkItems) ListEvents(context.Context, workitems.ListEventsQuery) ([]*workitems.Event, error) {
	return nil, b.unavailable("ListEvents")
}
func (b *unavailableWorkItems) BackendName() string { return b.name + "-unavailable" }
