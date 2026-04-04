package service

import (
	"context"
	"time"
)

// WorkspaceData represents the full workspace topology returned by the API.
type WorkspaceData struct {
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	Path             string               `json:"path"`
	Repos            []WorkspaceRepo      `json:"repos"`
	Groups           []string             `json:"groups"`
	Agents           []WorkspaceAgentInfo `json:"agents"`
	Workspaces       []WorkspaceSummary   `json:"workspaces"`
	WorkspaceOrder   []string             `json:"workspace_order,omitempty"`
	DefaultWorkspace string               `json:"default_workspace"`
}

// WorkspaceSummary provides a lightweight summary of a configured workspace.
type WorkspaceSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Active    bool   `json:"active"`
	RepoCount int    `json:"repo_count"`
	IsDefault bool   `json:"is_default"`
	Backend   string `json:"backend,omitempty"`
}

// WorkspaceRepo represents a repository within a workspace.
type WorkspaceRepo struct {
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	DefaultBranch string   `json:"default_branch"`
	Remote        string   `json:"remote"`
	SourceRepoID  string   `json:"source_repo_id,omitempty"`
	Groups        []string `json:"groups"`
}

// WorkspaceAgentInfo represents an agent's repo/group assignments.
type WorkspaceAgentInfo struct {
	Name       string   `json:"name"`
	Repos      []string `json:"repos"`
	RepoGroups []string `json:"repo_groups"`
	CrossRepo  bool     `json:"cross_repo"`
}

// WorkspaceCreateRequest is the JSON body for POST /api/workspaces.
type WorkspaceCreateRequest struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`       // "empty", "clone", "template"
	Repos     []string `json:"repos"`      // repo paths (for empty type)
	CloneURL  string   `json:"clone_url"`  // single git URL (backward compat)
	CloneURLs []string `json:"clone_urls"` // multiple git URLs (for clone type)
	Branch    string   `json:"branch"`     // optional branch name
	Path      string   `json:"path"`       // optional workspace directory override
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

// WorkspaceJobStatus represents the current state of a workspace creation job.
type WorkspaceJobStatus string

const (
	JobStatusRunning WorkspaceJobStatus = "running"
	JobStatusDone    WorkspaceJobStatus = "done"
	JobStatusFailed  WorkspaceJobStatus = "failed"
)

// WorkspaceJob is an immutable snapshot of a workspace creation job's state.
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

// PoolStats contains connection pool statistics.
// Mirrors daemon.PoolStats so the service API does not leak daemon types.
type PoolStats struct {
	Size      int  `json:"size"`
	Created   int  `json:"created"`
	Active    int  `json:"active"`
	Available int  `json:"available"`
	Closed    bool `json:"closed"`
}

// GetCreateWarnings returns collected warnings, or nil.
func GetCreateWarnings(ctx context.Context) []string {
	if w, ok := ctx.Value(createWarningsKey{}).(*[]string); ok && len(*w) > 0 {
		return *w
	}
	return nil
}
