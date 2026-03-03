package cli

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
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

// BDRunner wraps bd (beads) CLI calls.
type BDRunner interface {
	Run(dir string, args ...string) CommandResult
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
	Git    GitRunner
	Exec   ExecRunner
	FS     FileSystem
	Logger *slog.Logger
	Clock  func() time.Time
	BD     BDRunner
}

// --- default implementations ---

type defaultGitRunner struct{}

func (defaultGitRunner) Run(dir string, args ...string) CommandResult {
	return execCommand(dir, "git", args...)
}

func (defaultGitRunner) RunWithOutput(dir string, args ...string) error {
	return runGitWithOutputFunc(dir, args...)
}

type defaultExecRunner struct{}

func (defaultExecRunner) Run(dir, name string, args ...string) CommandResult {
	return execCommand(dir, name, args...)
}

type defaultBDRunner struct{}

func (defaultBDRunner) Run(dir string, args ...string) CommandResult {
	return execCommand(dir, "bd", args...)
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

// DefaultDeps returns a Deps populated with real (production) implementations.
func DefaultDeps() *Deps {
	return &Deps{
		Git:    defaultGitRunner{},
		Exec:   defaultExecRunner{},
		FS:     defaultFileSystem{},
		Logger: slog.Default(),
		Clock:  time.Now,
		BD:     defaultBDRunner{},
	}
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
// If none is set, it returns DefaultDeps() so callers never receive nil.
func GetDeps(cmd *cobra.Command) *Deps {
	if cmd == nil {
		return DefaultDeps()
	}
	ctx := cmd.Context()
	if ctx == nil {
		return DefaultDeps()
	}
	if d, ok := ctx.Value(depsKey).(*Deps); ok && d != nil {
		return d
	}
	return DefaultDeps()
}
