package issues

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"net/url"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// IssueBackendFn returns the active backend.IssueBackend. Consumed by
// HandleReadyWithBackend when no daemon pool is available (fleet mode).
// Defined locally to mirror the git package's identical-shape type
// without creating a package cross-import in production code.
//
// ctx carries the per-request workspace ID so cloud-mode wirings can route
// to a per-workspace fleet-db backend.
type IssueBackendFn func(ctx context.Context) backend.IssueBackend

// ReadyIssueWithParent extends Issue with parent info for /api/ready.
// This enables epic swimlane grouping in the Kanban view.
type ReadyIssueWithParent struct {
	*types.Issue
	Parent      *string `json:"parent,omitempty"`       // Parent issue ID (null for root-level issues)
	ParentTitle *string `json:"parent_title,omitempty"` // Parent issue title for display
	Repo        *string `json:"repo,omitempty"`         // Repository that owns this issue
}

// ReadyResponse wraps the ready issues data for JSON response.
type ReadyResponse struct {
	Success bool                    `json:"success"`
	Data    []*ReadyIssueWithParent `json:"data,omitempty"`
	Error   string                  `json:"error,omitempty"`
}

// readyFilter is the HTTP adapter's validated query contract. Keeping this
// transport-owned prevents the retired daemon RPC protocol from leaking into
// the direct IssueBackend port.
type readyFilter struct {
	Assignee    string
	Unassigned  bool
	Priority    *int
	Type        string
	Limit       int
	SortPolicy  string
	Labels      []string
	LabelsAny   []string
	ParentID    string
	MolType     string
	SourceRepos []string
}

// HandleReadyWithBackend serves ready issues through the owned IssueBackend
// port. It fails closed when composition did not provide that port.
func HandleReadyWithBackend(backendFn IssueBackendFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !serveReadyViaBackend(w, r, backendFn) {
			handler.WriteJSON(w, http.StatusServiceUnavailable, ReadyResponse{Success: false, Error: "issue backend not configured"})
		}
	}
}

// serveReadyViaBackend materializes a ready-style response from the supplied
// IssueBackend and writes it to w. Returns true when it served the request
// (including backend errors), false when no backend is wired so the caller
// can fall through to the pool-error path.
//
//nolint:funlen // Handler keeps the ready response shaping in one place.
func serveReadyViaBackend(w http.ResponseWriter, r *http.Request, backendFn IssueBackendFn) bool {
	if backendFn == nil {
		return false
	}
	be := backendFn(r.Context())
	if be == nil {
		return false
	}
	args, err := parseReadyParams(r)
	if err != nil {
		handler.WriteJSON(w, http.StatusBadRequest, ReadyResponse{
			Success: false,
			Error:   err.Error(),
		})
		return true
	}
	opts := backend.ReadyOpts{
		Assignee:    args.Assignee,
		Unassigned:  args.Unassigned,
		Priority:    args.Priority,
		Type:        args.Type,
		ParentID:    args.ParentID,
		Limit:       args.Limit,
		SortPolicy:  args.SortPolicy,
		Labels:      args.Labels,
		LabelsAny:   args.LabelsAny,
		MolType:     args.MolType,
		SourceRepos: args.SourceRepos,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	issues, err := be.Ready(ctx, opts)
	if err != nil {
		slog.Error("backend error in HandleReady", "err", err)
		handler.WriteJSON(w, http.StatusInternalServerError, ReadyResponse{
			Success: false,
			Error:   "failed to list ready issues",
		})
		return true
	}
	// Project each IssueData onto the pool-path wire shape. Parent/Repo are
	// surfaced from IssueData directly and ParentTitle is left nil.
	out := make([]*ReadyIssueWithParent, 0, len(issues))
	for i := range issues {
		d := &issues[i]
		iwp := &ReadyIssueWithParent{Issue: issueDataToTypesIssue(d)}
		if d.Parent != "" {
			p := d.Parent
			iwp.Parent = &p
		}
		if d.SourceRepo != "" {
			r := d.SourceRepo
			iwp.Repo = &r
		}
		out = append(out, iwp)
	}
	handler.WriteJSON(w, http.StatusOK, ReadyResponse{
		Success: true,
		Data:    out,
	})
	return true
}

// issueDataToTypesIssue projects a backend.IssueData into the slim
// *types.Issue shape that ReadyIssueWithParent embeds. Only the fields the
// backend slim projection populates are carried; unknown fields stay at
// their zero values (the FE already tolerates missing optional fields on a
// ready list item).
//
// Design must be carried: agents reading the ready queue through the API
// backend (LOOM_SERVER_URL set) apply the task router's has_design filter
// (ReadyToImplement = HasDesign && !needs-revision). Dropping it here made
// every ready task look design-less, so implementation agents could never
// claim work (perpetual NoWork) while planners — gated on !HasDesign — were
// unaffected. It is omitempty, so empty designs add nothing to the wire.
func issueDataToTypesIssue(d *backend.IssueData) *types.Issue {
	if d == nil {
		return nil
	}
	issue := &types.Issue{
		ID:               d.ID,
		Title:            d.Title,
		Status:           types.Status(d.Status),
		Priority:         d.Priority,
		IssueType:        types.IssueType(d.IssueType),
		Assignee:         d.Assignee,
		Owner:            d.Owner,
		Labels:           d.Labels,
		SourceRepo:       d.SourceRepo,
		Design:           d.Design,
		DesignArtifactID: d.DesignArtifactID,
		DesignFormat:     d.DesignFormat,
		HasDesign:        d.HasDesign || d.Design != "",
		CreatedAt:        d.CreatedAt,
		UpdatedAt:        d.UpdatedAt,
		DueAt:            d.DueAt,
		DeferUntil:       d.DeferUntil,
	}
	return issue
}

// parseReadyParams parses and validates ready-list query parameters.
func parseReadyParams(r *http.Request) (*readyFilter, error) {
	args := &readyFilter{}
	q := r.URL.Query()

	args.Assignee = handler.ParseStringParam(q, "assignee")
	args.Type = handler.ParseStringParam(q, "type")
	args.ParentID = handler.ParseStringParam(q, "parent_id")

	if err := parseReadyValidatedStrings(q, args); err != nil {
		return nil, err
	}

	var err error
	if args.Unassigned, err = handler.ParseBoolParam(q, "unassigned"); err != nil {
		return nil, err
	}
	if err := parseReadyIntParams(q, args); err != nil {
		return nil, err
	}

	args.Labels = handler.ParseArrayParam(q, "labels")
	args.LabelsAny = handler.ParseArrayParam(q, "labels_any")
	args.SourceRepos = handler.ParseArrayParam(q, "source_repos")

	return args, nil
}

// parseReadyValidatedStrings parses and validates mol_type and sort parameters.
func parseReadyValidatedStrings(q url.Values, args *readyFilter) error {
	if v := handler.ParseStringParam(q, "mol_type"); v != "" {
		if !types.MolType(v).IsValid() {
			return fmt.Errorf("invalid mol_type: %s (must be swarm, patrol, or work)", v)
		}
		args.MolType = v
	}
	if v := handler.ParseStringParam(q, "sort"); v != "" {
		if !types.SortPolicy(v).IsValid() {
			return fmt.Errorf("invalid sort policy: %s (must be hybrid, priority, or oldest)", v)
		}
		args.SortPolicy = v
	}
	return nil
}

// parseReadyIntParams parses priority and limit integer parameters.
func parseReadyIntParams(q url.Values, args *readyFilter) error {
	var err error
	if args.Priority, err = handler.ParseIntParam(q, "priority"); err != nil {
		return err
	}
	if args.Priority != nil && (*args.Priority < 0 || *args.Priority > 4) {
		return fmt.Errorf("priority must be between 0 and 4 (got %d)", *args.Priority)
	}
	limitPtr, err := handler.ParseIntParam(q, "limit")
	if err != nil {
		return err
	}
	if limitPtr != nil {
		if *limitPtr < 0 {
			return fmt.Errorf("limit must be non-negative (got %d)", *limitPtr)
		}
		args.Limit = *limitPtr
	}
	return nil
}
