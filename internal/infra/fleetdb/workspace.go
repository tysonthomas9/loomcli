package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// workspaceStore implements workspaceowner.WorkspaceStore against fleet-db's
// /api/v1/admin/workspaces endpoints. Bound to a parent *Client which
// owns the HTTP transport + auth state.
type workspaceStore struct{ client *Client }

var _ workspaceowner.WorkspaceStore = (*workspaceStore)(nil)

// workspaceWire is the JSON shape fleet-db emits for a workspace.
// Mirrors models.Workspace but lives here so loom doesn't import
// fleet-db packages directly.
type workspaceWire struct {
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Repos       []string  `json:"repos,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Optional fields fleet-db may add (state, default_branch, etc.).
	// Decoded if present, ignored if absent.
	State         string `json:"state,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	DesignFormat  string `json:"design_format,omitempty"`
}

func (w workspaceWire) toDomain() *workspaceowner.Workspace {
	return &workspaceowner.Workspace{
		Key:           w.Key,
		Name:          w.Name,
		Description:   w.Description,
		State:         workspaceowner.State(w.State),
		ErrorMessage:  w.ErrorMessage,
		DefaultBranch: w.DefaultBranch,
		DesignFormat:  w.DesignFormat,
		CreatedAt:     w.CreatedAt,
		UpdatedAt:     w.UpdatedAt,
	}
}

func (s *workspaceStore) Create(ctx context.Context, in workspaceowner.WorkspaceCreate) (*workspaceowner.Workspace, error) {
	body := struct {
		Key           string `json:"key"`
		Name          string `json:"name"`
		Description   string `json:"description,omitempty"`
		DefaultBranch string `json:"default_branch,omitempty"`
		DesignFormat  string `json:"design_format,omitempty"`
	}{
		Key:           in.Key,
		Name:          in.Name,
		Description:   in.Description,
		DefaultBranch: in.DefaultBranch,
		DesignFormat:  in.DesignFormat,
	}
	var resp workspaceWire
	if err := s.client.do(ctx, "POST", "/api/v1/admin/workspaces", body, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *workspaceStore) Get(ctx context.Context, key string) (*workspaceowner.Workspace, error) {
	var resp workspaceWire
	if err := s.client.do(ctx, "GET", "/api/v1/admin/workspaces/"+pathEscape(key), nil, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *workspaceStore) GetByName(ctx context.Context, name string) (*workspaceowner.Workspace, error) {
	// Fleet-db does not currently expose a name-lookup endpoint, so we
	// fall back to List + scan. List is cheap (workspace count is tiny);
	// upgrade to a dedicated endpoint if it ever becomes a hotspot.
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, ws := range all {
		if ws.Name == name {
			return ws, nil
		}
	}
	return nil, fmt.Errorf("fleetdb: workspace name %q: %w", name, persistence.ErrNotFound)
}

func (s *workspaceStore) List(ctx context.Context) ([]*workspaceowner.Workspace, error) {
	var resp struct {
		Workspaces []workspaceWire `json:"workspaces"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/admin/workspaces", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*workspaceowner.Workspace, 0, len(resp.Workspaces))
	for _, w := range resp.Workspaces {
		out = append(out, w.toDomain())
	}
	return out, nil
}

func (s *workspaceStore) Update(ctx context.Context, key string, patch workspaceowner.WorkspaceUpdate) (*workspaceowner.Workspace, error) {
	if patch.Name == nil &&
		patch.Description == nil &&
		patch.State == nil &&
		patch.ErrorMessage == nil &&
		patch.DefaultBranch == nil &&
		patch.DesignFormat == nil {
		return s.Get(ctx, key)
	}
	body := struct {
		Name          *string               `json:"name,omitempty"`
		Description   *string               `json:"description,omitempty"`
		State         *workspaceowner.State `json:"state,omitempty"`
		ErrorMessage  *string               `json:"error_message,omitempty"`
		DefaultBranch *string               `json:"default_branch,omitempty"`
		DesignFormat  *string               `json:"design_format,omitempty"`
	}{
		Name:          patch.Name,
		Description:   patch.Description,
		State:         patch.State,
		ErrorMessage:  patch.ErrorMessage,
		DefaultBranch: patch.DefaultBranch,
		DesignFormat:  patch.DesignFormat,
	}
	if err := s.client.do(ctx, "PATCH", "/api/v1/admin/workspaces/"+pathEscape(key), body, nil); err != nil {
		return nil, err
	}
	return s.Get(ctx, key)
}

func (s *workspaceStore) Delete(ctx context.Context, key string) error {
	return s.client.do(ctx, "DELETE", "/api/v1/admin/workspaces/"+pathEscape(key)+"?force=true", nil, nil)
}

type repoStore struct{ client *Client }

var _ workspaceowner.RepoStore = (*repoStore)(nil)

// repoWire mirrors fleet-db's models.Repo JSON shape.
type repoWire struct {
	WorkspaceKey  string    `json:"workspace_key"`
	Name          string    `json:"name"`
	RemoteURL     string    `json:"remote_url"`
	Remote        string    `json:"remote,omitempty"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	Groups        []string  `json:"groups,omitempty"`
	SourceRepoID  string    `json:"source_repo_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (r repoWire) toDomain() *workspaceowner.Repository {
	return &workspaceowner.Repository{
		WorkspaceKey:  r.WorkspaceKey,
		Name:          r.Name,
		RemoteURL:     r.RemoteURL,
		Remote:        r.Remote,
		DefaultBranch: r.DefaultBranch,
		Groups:        r.Groups,
		SourceRepoID:  r.SourceRepoID,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

func (s *repoStore) Create(ctx context.Context, in workspaceowner.RepoCreate) (*workspaceowner.Repository, error) {
	body := struct {
		Name          string   `json:"name"`
		RemoteURL     string   `json:"remote_url"`
		Remote        string   `json:"remote,omitempty"`
		DefaultBranch string   `json:"default_branch,omitempty"`
		Groups        []string `json:"groups,omitempty"`
		SourceRepoID  string   `json:"source_repo_id,omitempty"`
	}{
		Name:          in.Name,
		RemoteURL:     in.RemoteURL,
		Remote:        in.Remote,
		DefaultBranch: in.DefaultBranch,
		Groups:        in.Groups,
		SourceRepoID:  in.SourceRepoID,
	}
	var resp repoWire
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/repos", body, &resp); err != nil {
		return nil, err
	}
	// FleetDB creates the first-class repository and its legacy workspace
	// admission mapping in one backend commit. A follow-up workspace PATCH
	// would expose a partially admitted repository between the two calls.
	return resp.toDomain(), nil
}

func (s *repoStore) Get(ctx context.Context, ws, name string) (*workspaceowner.Repository, error) {
	var resp repoWire
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/repos/"+pathEscape(name), nil, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *repoStore) List(ctx context.Context, ws string) ([]*workspaceowner.Repository, error) {
	var resp struct {
		Repos []repoWire `json:"repos"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/repos", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*workspaceowner.Repository, 0, len(resp.Repos))
	for _, r := range resp.Repos {
		out = append(out, r.toDomain())
	}
	return out, nil
}

func (s *repoStore) Update(ctx context.Context, ws, name string, patch workspaceowner.RepoUpdate) (*workspaceowner.Repository, error) {
	body := struct {
		RemoteURL     *string   `json:"remote_url,omitempty"`
		Remote        *string   `json:"remote,omitempty"`
		DefaultBranch *string   `json:"default_branch,omitempty"`
		Groups        *[]string `json:"groups,omitempty"`
		SourceRepoID  *string   `json:"source_repo_id,omitempty"`
	}{
		RemoteURL:     patch.RemoteURL,
		Remote:        patch.Remote,
		DefaultBranch: patch.DefaultBranch,
		Groups:        patch.Groups,
		SourceRepoID:  patch.SourceRepoID,
	}
	var resp repoWire
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/repos/"+pathEscape(name), body, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *repoStore) Delete(ctx context.Context, ws, name string) error {
	// FleetDB's guarded repository command removes the first-class entity and
	// its legacy workspace admission mapping in one backend commit. A follow-up
	// workspace PATCH would reintroduce a race in which new work can be admitted
	// between the two calls.
	return s.client.do(ctx, "DELETE", "/api/v1/"+pathEscape(ws)+"/repos/"+pathEscape(name), nil, nil)
}

// The atomic move transport is colocated with workspace/repository transport
// because it is the only Fleet command spanning two workspace coordinates.
// It remains exposed through Client.WorkItemMoves rather than either owner
// store, so neither Workspace nor Work Items gains the low-level command.
var (
	ErrWorkItemMoveRevisionConflict    = errors.New("fleetdb: work item move revision conflict")
	ErrWorkItemMoveIdempotencyConflict = errors.New("fleetdb: work item move idempotency conflict")
	ErrWorkItemMoveIneligible          = errors.New("fleetdb: work item move ineligible")
	ErrWorkItemMoveForbidden           = errors.New("fleetdb: work item move forbidden")
)

type WorkItemMoveInput struct {
	TargetWorkspace        string    `json:"target_workspace"`
	ExpectedSourceRevision time.Time `json:"expected_source_revision"`
	RequestID              string    `json:"request_id"`
}

type WorkItemReference struct {
	Workspace string `json:"workspace"`
	IssueID   string `json:"issue_id"`
}

type WorkItemMoveIssue struct {
	ID        string             `json:"id"`
	Workspace string             `json:"workspace"`
	MovedTo   *WorkItemReference `json:"moved_to,omitempty"`
	MovedFrom *WorkItemReference `json:"moved_from,omitempty"`
}

type WorkItemMoveResult struct {
	Source   *WorkItemMoveIssue `json:"source"`
	Target   *WorkItemMoveIssue `json:"target"`
	Replayed bool               `json:"replayed"`
}

type WorkItemMoveTransport interface {
	MoveWorkItem(context.Context, string, string, WorkItemMoveInput) (*WorkItemMoveResult, error)
}

type workItemMoveTransport struct{ client fleetRequester }

func newWorkItemMoveTransport(requester fleetRequester) WorkItemMoveTransport {
	return &workItemMoveTransport{client: requester}
}

func (transport *workItemMoveTransport) MoveWorkItem(
	ctx context.Context,
	sourceWorkspace,
	sourceIssueID string,
	input WorkItemMoveInput,
) (*WorkItemMoveResult, error) {
	sourceWorkspace = strings.TrimSpace(sourceWorkspace)
	sourceIssueID = strings.TrimSpace(sourceIssueID)
	input.TargetWorkspace = strings.TrimSpace(input.TargetWorkspace)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.ExpectedSourceRevision = input.ExpectedSourceRevision.UTC()
	if transport == nil || transport.client == nil {
		return nil, fmt.Errorf("move work item transport is unavailable: %w", persistence.ErrUnavailable)
	}
	if sourceWorkspace == "" || sourceIssueID == "" || input.TargetWorkspace == "" ||
		input.TargetWorkspace == sourceWorkspace || input.ExpectedSourceRevision.IsZero() ||
		input.RequestID == "" || len(input.RequestID) > 200 {
		return nil, fmt.Errorf("invalid work item move intent: %w", persistence.ErrInvalid)
	}
	var result WorkItemMoveResult
	path := "/api/v1/" + url.PathEscape(sourceWorkspace) + "/issues/" + url.PathEscape(sourceIssueID) + "/move"
	if err := transport.client.Do(ctx, http.MethodPost, path, input, &result); err != nil {
		return nil, fmt.Errorf("move work item %s/%s: %w", sourceWorkspace, sourceIssueID, err)
	}
	return &result, nil
}
