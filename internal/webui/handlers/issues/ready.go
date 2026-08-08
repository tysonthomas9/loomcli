package issues

import (
	"fmt"
	"net/http"

	"net/url"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// ReadyIssueWithParent extends Issue with parent info for /api/ready.
// This enables epic swimlane grouping in the Kanban view.
type ReadyIssueWithParent struct {
	*workitems.IssueSummary
	Parent      *string `json:"parent,omitempty"`       // Parent issue ID (null for root-level issues)
	ParentTitle *string `json:"parent_title,omitempty"` // Parent issue title for display
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

// HandleReadyWorkItems serves ready issues through the Work Items owner query.
func HandleReadyWorkItems(queries workitems.ReadyQueries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if queries == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, ReadyResponse{Success: false, Error: "issue backend not configured"})
			return
		}
		args, err := parseReadyParams(r)
		if err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, ReadyResponse{Success: false, Error: err.Error()})
			return
		}
		values, err := queries.Ready(r.Context(), workitems.AvailabilityQuery{
			Assignee: args.Assignee, Unassigned: args.Unassigned, Priority: args.Priority,
			IssueType: args.Type, ParentID: args.ParentID, Limit: args.Limit,
			SortPolicy: args.SortPolicy, Labels: args.Labels, LabelsAny: args.LabelsAny,
			MolType: args.MolType, SourceRepos: args.SourceRepos,
		})
		if err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		out := make([]*ReadyIssueWithParent, 0, len(values))
		for index := range values {
			value := values[index]
			item := &ReadyIssueWithParent{IssueSummary: &value}
			if value.Parent != "" {
				parent := value.Parent
				item.Parent = &parent
			}
			out = append(out, item)
		}
		handler.WriteJSON(w, http.StatusOK, ReadyResponse{Success: true, Data: out})
	}
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
