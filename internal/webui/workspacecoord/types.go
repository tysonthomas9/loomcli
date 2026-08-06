package workspacecoord

import (
	"context"
	"strings"
	"time"
)

// WorkspaceCreateRequest is the JSON body for POST /api/workspaces.
type WorkspaceCreateRequest struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`       // "empty", "clone", "template"
	Repos     []string `json:"repos"`      // repo paths (for empty type)
	CloneURLs []string `json:"clone_urls"` // multiple git URLs (for clone type)
	Branch    string   `json:"branch"`     // optional branch name
	Path      string   `json:"path"`       // optional workspace directory override
}

// WorkspaceAddReposRequest is the JSON body for POST /api/workspaces/{ws}/repos.
type WorkspaceAddReposRequest struct {
	WorkspaceID string   `json:"-"`
	Repos       []string `json:"repos"`      // existing local repo paths
	CloneURLs   []string `json:"clone_urls"` // remote git URLs to clone into the workspace
	Branch      string   `json:"branch"`
}

// BackendConfigData is the response payload for workspace backend settings.
type BackendConfigData struct {
	Backend   string                 `json:"backend"`
	Source    string                 `json:"source"`
	Available []string               `json:"available"`
	Agents    []AgentBackendOverride `json:"agents"`
}

// AgentBackendOverride represents a per-agent backend override.
type AgentBackendOverride struct {
	Worktree string `json:"worktree"`
	Role     string `json:"role"`
	Backend  string `json:"backend"`
}

// WorkspaceKeyFromName derives the fleet-db workspace key used by store-backed
// web workspace creation. It intentionally mirrors fleet-db's key regex:
// uppercase letters/digits/hyphens, starts with a letter, max 32 chars.
func WorkspaceKeyFromName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteByte('-')
		}
	}
	key := strings.Trim(b.String(), "-")
	if key == "" {
		key = "W"
	}
	if key[0] < 'A' || key[0] > 'Z' {
		key = "W-" + key
	}
	if len(key) > 32 {
		key = strings.TrimRight(key[:32], "-")
	}
	if key == "" || key[0] < 'A' || key[0] > 'Z' {
		key = "W"
	}
	return key
}

// WorkspaceCreateResult carries data produced during workspace creation,
// eliminating the need for a post-creation config re-read.
type WorkspaceCreateResult struct {
	WorkspaceID   string // stable UUID assigned during creation
	WorkspacePath string // absolute path to the workspace directory
}

// WorkspaceCreateFn is the function signature for creating a workspace.
// Injected at server startup to decouple webui from CLI internals.
type WorkspaceCreateFn func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error)

// WorkspaceAddReposFn attaches existing local git repos or cloned remote repos
// to a workspace.
type WorkspaceAddReposFn func(ctx context.Context, req WorkspaceAddReposRequest) (WorkspaceCreateResult, error)

// WorkspaceJobStatus represents the current state of a workspace mutation job.
type WorkspaceJobStatus string

const (
	JobStatusRunning WorkspaceJobStatus = "running"
	JobStatusDone    WorkspaceJobStatus = "done"
	JobStatusFailed  WorkspaceJobStatus = "failed"
)

// WorkspaceJob is an immutable snapshot of a workspace mutation job's state.
type WorkspaceJob struct {
	ID          string             `json:"id"`
	Status      WorkspaceJobStatus `json:"status"`
	Progress    string             `json:"progress,omitempty"`
	WorkspaceID string             `json:"workspace_id,omitempty"`
	Error       string             `json:"error,omitempty"`
	CompletedAt time.Time          `json:"-"` // zero while running; set on done/failed
}

type createWarningsKey struct{}

// WithCreateWarnings returns a child context carrying an empty warnings collector.
func WithCreateWarnings(ctx context.Context) context.Context {
	w := &[]string{}
	return context.WithValue(ctx, createWarningsKey{}, w)
}

// AddCreateWarning appends a non-fatal warning to the context's collector.
// No-op if the context has no collector.
func AddCreateWarning(ctx context.Context, msg string) {
	if w, ok := ctx.Value(createWarningsKey{}).(*[]string); ok {
		*w = append(*w, msg)
	}
}

// GetCreateWarnings returns collected warnings, or nil.
func GetCreateWarnings(ctx context.Context) []string {
	if w, ok := ctx.Value(createWarningsKey{}).(*[]string); ok && len(*w) > 0 {
		return *w
	}
	return nil
}
