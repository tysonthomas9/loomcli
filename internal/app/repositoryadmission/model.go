package repositoryadmission

import (
	"context"
	"strings"
	"time"
)

// CreateCommand is the transport-neutral intent for creating a Workspace and
// optionally admitting cloned repositories in the same recoverable workflow.
type CreateCommand struct {
	Name      string
	Type      string
	Repos     []string
	CloneURLs []string
	Branch    string
	Path      string
}

// AddRepositoriesCommand is the transport-neutral intent for admitting local
// or cloned repositories to an existing Workspace.
type AddRepositoriesCommand struct {
	WorkspaceID string
	Repos       []string
	CloneURLs   []string
	Branch      string
}

// Result identifies the Workspace materialized by a synchronous command.
type Result struct {
	WorkspaceID   string
	WorkspacePath string
}

// RepositoryPlacement is the credential-free machine-local result of
// materializing one Workspace Repository Reference.
type RepositoryPlacement struct {
	Name          string
	Path          string
	Remote        string
	DefaultBranch string
	SourceRepoID  string
}

// CreateFunc and AddRepositoriesFunc are composition seams for the synchronous
// command variants used by local CLI and delivery adapters.
type CreateFunc func(context.Context, CreateCommand) (Result, error)
type AddRepositoriesFunc func(context.Context, AddRepositoriesCommand) (Result, error)

// Admission is the small durable interface used by asynchronous delivery.
// Every accepted command returns an opaque FleetDB admission ID; Get projects
// only that durable record and never infers status from process-local state.
type Admission interface {
	StartCreate(context.Context, CreateCommand) (string, error)
	StartAddRepositories(context.Context, AddRepositoriesCommand) (string, error)
	Get(context.Context, string) (*Status, bool, error)
}

// Executor is the synchronous interface used by local commands and local-only
// repository attachment. Clone work still crosses the same durable workflow.
type Executor interface {
	Create(context.Context, CreateCommand) (Result, error)
	AddRepositories(context.Context, AddRepositoriesCommand) (Result, error)
}

// State is the durable, UI-visible repository-admission state.
type State string

const (
	StateRunning State = "running"
	StateDone    State = "done"
	StateFailed  State = "failed"
)

// Status is an immutable projection of one durable FleetDB admission.
type Status struct {
	ID          string
	State       State
	Progress    string
	WorkspaceID string
	Error       string
	CompletedAt time.Time
}

// WorkspaceKey derives the FleetDB Workspace key from a display name.
func WorkspaceKey(name string) string {
	var key strings.Builder
	for _, character := range strings.ToUpper(name) {
		switch {
		case character >= 'A' && character <= 'Z', character >= '0' && character <= '9':
			key.WriteRune(character)
		case character == '-' || character == '_' || character == '.':
			key.WriteByte('-')
		}
	}
	value := strings.Trim(key.String(), "-")
	if value == "" {
		value = "W"
	}
	if value[0] < 'A' || value[0] > 'Z' {
		value = "W-" + value
	}
	if len(value) > 32 {
		value = strings.TrimRight(value[:32], "-")
	}
	if value == "" || value[0] < 'A' || value[0] > 'Z' {
		return "W"
	}
	return value
}

type warningsKey struct{}

// WithWarnings returns a child context carrying an empty non-fatal warning
// collector for synchronous Workspace creation.
func WithWarnings(ctx context.Context) context.Context {
	warnings := &[]string{}
	return context.WithValue(ctx, warningsKey{}, warnings)
}

// AddWarning records a non-fatal materialization warning when the caller
// supplied a collector.
func AddWarning(ctx context.Context, message string) {
	if warnings, ok := ctx.Value(warningsKey{}).(*[]string); ok {
		*warnings = append(*warnings, message)
	}
}

// Warnings returns the collected non-fatal warnings.
func Warnings(ctx context.Context) []string {
	if warnings, ok := ctx.Value(warningsKey{}).(*[]string); ok && len(*warnings) > 0 {
		return append([]string(nil), (*warnings)...)
	}
	return nil
}
