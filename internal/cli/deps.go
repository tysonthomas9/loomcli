package cli

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

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

// BDRunner wraps bd (beads) CLI calls.
type BDRunner interface {
	Run(dir string, args ...string) CommandResult
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
	Git      GitRunner
	Exec     ExecRunner
	FS       FileSystem
	Logger   *slog.Logger
	Clock    func() time.Time
	Tracker  IssueTracker
	LookPath func(file string) (string, error)
	ExecCtx  ExecContextRunner
	Agent    AgentInvoker
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
	bdRunner := defaultBDRunnerImpl{}
	return &Deps{
		Git:      defaultGitRunner{},
		Exec:     defaultExecRunner{},
		FS:       defaultFileSystem{},
		Logger:   slog.Default(),
		Clock:    time.Now,
		Tracker:  newBdBackend(bdRunner, GetBeadsDir()),
		LookPath: exec.LookPath,
		ExecCtx:  defaultExecContextRunner{},
		Agent:    registryAgentInvoker{},
	}
}

// defaultBDRunnerImpl implements BDRunner by shelling out to the bd CLI.
// Used only as the internal runner for bdBackend in DefaultDeps().
type defaultBDRunnerImpl struct{}

func (defaultBDRunnerImpl) Run(dir string, args ...string) CommandResult {
	return defaultDeps.Exec.Run(dir, "bd", args...)
}

// defaultDeps is the package-level Deps instance used by backward-compatible
// wrapper functions (RunGitCommand, fetchReadyIssues, etc.). Initialized to
// DefaultDeps() so that production code works without explicit wiring.
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
