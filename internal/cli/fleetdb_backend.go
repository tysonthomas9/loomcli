// Package cli — fleetDBBackend wraps an RPC client to communicate with a running
// daemon instance. It implements IssueTracker via typed methods that make RPC calls,
// enabling callers to operate against fleet-db without shelling out to the bd CLI.
//
// This is distinct from Backend/StreamingBackend (backend.go, backend_capabilities.go)
// which handle AI agent invocation. fleetDBBackend handles issue data operations only.
package cli

import (
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// fleetDBClient is a narrow interface wrapping the RPC methods that fleetDBBackend
// needs. The production *rpc.Client satisfies this interface. Tests provide a mock.
// This follows the webui pattern (see internal/webui/handlers_ready.go readyClient).
type fleetDBClient interface {
	Ready(args *rpc.ReadyArgs) (*rpc.Response, error)
	List(args *rpc.ListArgs) (*rpc.Response, error)
	Show(args *rpc.ShowArgs) (*rpc.Response, error)
	Blocked(args *rpc.BlockedArgs) (*rpc.Response, error)
	Stats() (*rpc.Response, error)
	Update(args *rpc.UpdateArgs) (*rpc.Response, error)
	CloseIssue(args *rpc.CloseArgs) (*rpc.Response, error)
}

// fleetDBBackend wraps an RPC client and workspace string to implement the
// IssueTracker interface via typed methods.
type fleetDBBackend struct {
	client    fleetDBClient
	workspace string
}

// newFleetDBBackend creates a new fleetDBBackend with the given RPC client and workspace.
func newFleetDBBackend(client fleetDBClient, workspace string) *fleetDBBackend { //nolint:unparam // workspace will vary when wired by task .6
	return &fleetDBBackend{
		client:    client,
		workspace: workspace,
	}
}

// --- type conversion functions ---

// issueToBdIssue converts a types.Issue to a BdIssue with explicit field mapping.
// Labels and Dependencies are guaranteed non-nil slices for JSON serialization.
func issueToBdIssue(issue *types.Issue) BdIssue {
	labels := make([]string, 0)
	if len(issue.Labels) > 0 {
		labels = issue.Labels
	}

	deps := make([]Dependency, 0)
	for _, d := range issue.Dependencies {
		if d != nil {
			deps = append(deps, convertDependency(d))
		}
	}

	return BdIssue{
		ID:           issue.ID,
		Title:        issue.Title,
		Status:       string(issue.Status),
		Priority:     issue.Priority,
		IssueType:    string(issue.IssueType),
		Design:       issue.Design,
		Assignee:     issue.Assignee,
		Labels:       labels,
		Dependencies: deps,
		SourceRepo:   issue.SourceRepo,
	}
}

// convertDependency converts a types.Dependency to a cli.Dependency.
// CRITICAL: types.Dependency.CreatedAt is time.Time, cli.Dependency.CreatedAt is string.
// types.Dependency.Type is DependencyType (typed enum), cli.Dependency.Type is string.
// Metadata and ThreadID fields are dropped (not present in cli.Dependency).
func convertDependency(dep *types.Dependency) Dependency {
	return Dependency{
		IssueID:     dep.IssueID,
		DependsOnID: dep.DependsOnID,
		Type:        string(dep.Type),
		CreatedAt:   dep.CreatedAt.Format(time.RFC3339),
		CreatedBy:   dep.CreatedBy,
	}
}

// issueDetailsToBdIssue converts a types.IssueDetails (from Show) to a BdIssue.
// IssueDetails has Dependencies as []*IssueWithDependencyMetadata, not []*Dependency.
// The dependency type comes from the wrapper's DependencyType field.
func issueDetailsToBdIssue(details *types.IssueDetails) BdIssue {
	labels := make([]string, 0)
	if len(details.Labels) > 0 {
		labels = details.Labels
	}

	deps := make([]Dependency, 0)
	for _, iwdm := range details.Dependencies {
		if iwdm == nil {
			continue
		}
		deps = append(deps, Dependency{
			IssueID:     details.Issue.ID,
			DependsOnID: iwdm.Issue.ID,
			Type:        string(iwdm.DependencyType),
			CreatedAt:   iwdm.Issue.CreatedAt.Format(time.RFC3339),
			CreatedBy:   iwdm.Issue.CreatedBy,
		})
	}

	return BdIssue{
		ID:           details.Issue.ID,
		Title:        details.Issue.Title,
		Status:       string(details.Issue.Status),
		Priority:     details.Issue.Priority,
		IssueType:    string(details.Issue.IssueType),
		Design:       details.Issue.Design,
		Assignee:     details.Issue.Assignee,
		Labels:       labels,
		Dependencies: deps,
		SourceRepo:   details.Issue.SourceRepo,
	}
}

// statisticsToBdStats converts a types.Statistics to a BdStats.
func statisticsToBdStats(stats *types.Statistics) BdStats {
	var bdStats BdStats
	bdStats.Summary.TotalIssues = stats.TotalIssues
	bdStats.Summary.OpenIssues = stats.OpenIssues
	bdStats.Summary.ClosedIssues = stats.ClosedIssues
	bdStats.Summary.InProgressIssues = stats.InProgressIssues
	bdStats.Summary.BlockedIssues = stats.BlockedIssues
	bdStats.Summary.DeferredIssues = stats.DeferredIssues
	bdStats.Summary.TombstoneIssues = stats.TombstoneIssues
	bdStats.Summary.PinnedIssues = stats.PinnedIssues
	return bdStats
}
