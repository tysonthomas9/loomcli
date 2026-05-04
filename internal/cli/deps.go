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
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// GitRunner wraps git command execution.
type GitRunner interface {
	// Run executes a git command and captures output.
	Run(dir string, args ...string) CommandResult
	// RunWithOutput executes a git command, streaming to stdout/stderr.
	RunWithOutput(dir string, args ...string) error
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
}

// --- default implementations ---

type defaultGitRunner struct{}

func (defaultGitRunner) Run(dir string, args ...string) CommandResult {
	return defaultDeps.Exec.Run(dir, "git", args...)
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
func DefaultDeps() *Deps {
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

	return &Deps{
		Git:          defaultGitRunner{},
		Exec:         defaultExecRunner{},
		FS:           defaultFileSystem{},
		Logger:       slog.Default(),
		Clock:        time.Now,
		IssueBackend: issueBackend,
		LookPath:     exec.LookPath,
		ExecCtx:      defaultExecContextRunner{},
		Agent:        registryAgentInvoker{},
	}
}

// defaultDeps is the package-level Deps instance used by package helpers.
// Initialized to DefaultDeps() so production code works without explicit wiring.
var defaultDeps = DefaultDeps()

// --- cobra context helpers ---

type depsKeyType struct{}

var depsKey = depsKeyType{}

// WithDeps stores a *Deps in the given context.
func WithDeps(ctx context.Context, d *Deps) context.Context {
	return context.WithValue(ctx, depsKey, d)
}

// GetDeps extracts the *Deps from a cobra command's context.
// If none is set, it returns defaultDeps (the package singleton) so that
// test-time swaps via installExecMock/etc. are visible to all callers.
func GetDeps(cmd *cobra.Command) *Deps {
	if cmd == nil {
		return defaultDeps
	}
	ctx := cmd.Context()
	if ctx == nil {
		return defaultDeps
	}
	if d, ok := ctx.Value(depsKey).(*Deps); ok && d != nil {
		return d
	}
	return defaultDeps
}

// TestingGetDefaultDeps returns the package-level defaultDeps for use by test
// packages that need to swap global state. Production code should use GetDeps.
func TestingGetDefaultDeps() *Deps {
	return defaultDeps
}

// fleetDBIssueBackend lazily opens fleet-db for IssueBackend calls. It avoids
// spawning embedded fleet-db during package init while still making fleet-db
// the default CLI issue backend.
type fleetDBIssueBackend struct{}

var _ backend.IssueBackend = (*fleetDBIssueBackend)(nil)

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
		APIKey:      os.Getenv(bootstrap.EnvFleetDBAPIKey),
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

func (b *fleetDBIssueBackend) Ready(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	var out []backend.IssueData
	err := b.withBackend(ctx, "Ready", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.Ready(ctx, opts)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) Blocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	var out []backend.IssueData
	err := b.withBackend(ctx, "Blocked", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.Blocked(ctx, opts)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) Stats(ctx context.Context) (*backend.StatsData, error) {
	var out *backend.StatsData
	err := b.withBackend(ctx, "Stats", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.Stats(ctx)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) Count(ctx context.Context, opts backend.CountOpts) (int, error) {
	var out int
	err := b.withBackend(ctx, "Count", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.Count(ctx, opts)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) GetChildren(ctx context.Context, id string) ([]backend.IssueData, error) {
	var out []backend.IssueData
	err := b.withBackend(ctx, "GetChildren", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.GetChildren(ctx, id)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) SearchIssues(ctx context.Context, query string, limit int) ([]backend.IssueData, error) {
	var out []backend.IssueData
	err := b.withBackend(ctx, "SearchIssues", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.SearchIssues(ctx, query, limit)
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

func (b *fleetDBIssueBackend) DeferIssue(ctx context.Context, id string, until time.Time) error {
	return b.withBackend(ctx, "DeferIssue", func(ib backend.IssueBackend) error {
		return ib.DeferIssue(ctx, id, until)
	})
}

func (b *fleetDBIssueBackend) UndeferIssue(ctx context.Context, id string) error {
	return b.withBackend(ctx, "UndeferIssue", func(ib backend.IssueBackend) error {
		return ib.UndeferIssue(ctx, id)
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

func (b *fleetDBIssueBackend) AddDependency(ctx context.Context, params backend.DepAddParams) error {
	return b.withBackend(ctx, "AddDependency", func(ib backend.IssueBackend) error {
		return ib.AddDependency(ctx, params)
	})
}

func (b *fleetDBIssueBackend) RemoveDependency(ctx context.Context, params backend.DepRemoveParams) error {
	return b.withBackend(ctx, "RemoveDependency", func(ib backend.IssueBackend) error {
		return ib.RemoveDependency(ctx, params)
	})
}

func (b *fleetDBIssueBackend) AddLabel(ctx context.Context, id string, label string) error {
	return b.withBackend(ctx, "AddLabel", func(ib backend.IssueBackend) error {
		return ib.AddLabel(ctx, id, label)
	})
}

func (b *fleetDBIssueBackend) RemoveLabel(ctx context.Context, id string, label string) error {
	return b.withBackend(ctx, "RemoveLabel", func(ib backend.IssueBackend) error {
		return ib.RemoveLabel(ctx, id, label)
	})
}

func (b *fleetDBIssueBackend) ListComments(ctx context.Context, id string) ([]backend.CommentData, error) {
	var out []backend.CommentData
	err := b.withBackend(ctx, "ListComments", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.ListComments(ctx, id)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) AddComment(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	var out *backend.CommentData
	err := b.withBackend(ctx, "AddComment", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.AddComment(ctx, params)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) ListEvents(ctx context.Context, id string, limit int) ([]backend.EventData, error) {
	var out []backend.EventData
	err := b.withBackend(ctx, "ListEvents", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.ListEvents(ctx, id, limit)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) Batch(ctx context.Context, ops []backend.BatchOp) ([]backend.BatchResult, error) {
	var out []backend.BatchResult
	err := b.withBackend(ctx, "Batch", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.Batch(ctx, ops)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) GetMutations(ctx context.Context, sinceMs int64) ([]backend.MutationData, error) {
	var out []backend.MutationData
	err := b.withBackend(ctx, "GetMutations", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.GetMutations(ctx, sinceMs)
		return err
	})
	return out, err
}

func (b *fleetDBIssueBackend) WaitForMutations(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
	var out []backend.MutationData
	err := b.withBackend(ctx, "WaitForMutations", func(ib backend.IssueBackend) error {
		var err error
		out, err = ib.WaitForMutations(ctx, sinceMs, timeoutMs)
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
func (b *unavailableIssueBackend) Ready(context.Context, backend.ReadyOpts) ([]backend.IssueData, error) {
	return nil, b.unavailable("Ready")
}
func (b *unavailableIssueBackend) Blocked(context.Context, backend.BlockedOpts) ([]backend.IssueData, error) {
	return nil, b.unavailable("Blocked")
}
func (b *unavailableIssueBackend) Stats(context.Context) (*backend.StatsData, error) {
	return nil, b.unavailable("Stats")
}
func (b *unavailableIssueBackend) Count(context.Context, backend.CountOpts) (int, error) {
	return 0, b.unavailable("Count")
}
func (b *unavailableIssueBackend) GetChildren(context.Context, string) ([]backend.IssueData, error) {
	return nil, b.unavailable("GetChildren")
}
func (b *unavailableIssueBackend) SearchIssues(context.Context, string, int) ([]backend.IssueData, error) {
	return nil, b.unavailable("SearchIssues")
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
func (b *unavailableIssueBackend) DeferIssue(context.Context, string, time.Time) error {
	return b.unavailable("DeferIssue")
}
func (b *unavailableIssueBackend) UndeferIssue(context.Context, string) error {
	return b.unavailable("UndeferIssue")
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
func (b *unavailableIssueBackend) AddDependency(context.Context, backend.DepAddParams) error {
	return b.unavailable("AddDependency")
}
func (b *unavailableIssueBackend) RemoveDependency(context.Context, backend.DepRemoveParams) error {
	return b.unavailable("RemoveDependency")
}
func (b *unavailableIssueBackend) AddLabel(context.Context, string, string) error {
	return b.unavailable("AddLabel")
}
func (b *unavailableIssueBackend) RemoveLabel(context.Context, string, string) error {
	return b.unavailable("RemoveLabel")
}
func (b *unavailableIssueBackend) ListComments(context.Context, string) ([]backend.CommentData, error) {
	return nil, b.unavailable("ListComments")
}
func (b *unavailableIssueBackend) AddComment(context.Context, backend.CommentAddParams) (*backend.CommentData, error) {
	return nil, b.unavailable("AddComment")
}
func (b *unavailableIssueBackend) ListEvents(context.Context, string, int) ([]backend.EventData, error) {
	return nil, b.unavailable("ListEvents")
}
func (b *unavailableIssueBackend) Batch(context.Context, []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, b.unavailable("Batch")
}
func (b *unavailableIssueBackend) GetMutations(context.Context, int64) ([]backend.MutationData, error) {
	return nil, b.unavailable("GetMutations")
}
func (b *unavailableIssueBackend) WaitForMutations(context.Context, int64, int64) ([]backend.MutationData, error) {
	return nil, b.unavailable("WaitForMutations")
}
func (b *unavailableIssueBackend) BackendName() string { return b.name + "-unavailable" }
