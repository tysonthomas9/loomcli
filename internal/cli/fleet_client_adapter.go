package cli

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tysonthomas9/fleet-db/pkg/client"
)

// fleetClientAdapter implements FleetService by wrapping pkg/client.Client,
// converting between fleet-db domain types and loomcli types.
type fleetClientAdapter struct {
	client    client.Client
	workspace string
	logger    *slog.Logger
}

func newFleetClientAdapter(c client.Client, workspace string, logger *slog.Logger) *fleetClientAdapter {
	return &fleetClientAdapter{
		client:    c,
		workspace: workspace,
		logger:    logger,
	}
}

// --- Queries ---

func (a *fleetClientAdapter) GetReady(ctx context.Context, limit int, parentID string) ([]BdIssue, error) {
	resp, err := a.client.GetReady(ctx, a.workspace, &client.ReadyOptions{
		Limit:    limit,
		ParentID: parentID,
	})
	if err != nil {
		return nil, err
	}
	return clientIssuesToBdIssues(resp.Issues), nil
}

func (a *fleetClientAdapter) ListIssues(ctx context.Context, status, issueType, assignee string, limit int) ([]BdIssue, error) {
	resp, err := a.client.ListIssues(ctx, a.workspace, &client.ListIssuesOptions{
		Status:   status,
		Type:     issueType,
		Assignee: assignee,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	return clientIssuesToBdIssues(resp.Issues), nil
}

func (a *fleetClientAdapter) GetBlocked(ctx context.Context) ([]BdIssue, error) {
	resp, err := a.client.GetBlocked(ctx, a.workspace)
	if err != nil {
		return nil, err
	}
	// BlockedListResponse has []BlockedIssue; extract the Issue pointer from each.
	issues := make([]*client.Issue, 0, len(resp.Issues))
	for i := range resp.Issues {
		if resp.Issues[i].Issue != nil {
			issues = append(issues, resp.Issues[i].Issue)
		}
	}
	return clientIssuesToBdIssues(issues), nil
}

func (a *fleetClientAdapter) CountByStatus(ctx context.Context) (BdStats, error) {
	resp, err := a.client.CountIssues(ctx, a.workspace, &client.CountIssuesOptions{
		GroupBy: "status",
	})
	if err != nil {
		return BdStats{}, err
	}
	return countResponseToBdStats(resp), nil
}

func (a *fleetClientAdapter) GetIssue(ctx context.Context, id string) (*BdIssue, error) {
	issue, err := a.client.GetIssue(ctx, a.workspace, id)
	if err != nil {
		return nil, err
	}
	result := clientIssueToBdIssue(issue)
	return &result, nil
}

func (a *fleetClientAdapter) GetIssueText(ctx context.Context, id string) (string, error) {
	issue, err := a.client.GetIssue(ctx, a.workspace, id)
	if err != nil {
		return "", err
	}
	return formatIssueText(issue), nil
}

func (a *fleetClientAdapter) GetDependencies(ctx context.Context, id string) ([]Dependency, error) {
	resp, err := a.client.ListDependencies(ctx, a.workspace, id)
	if err != nil {
		return nil, err
	}
	return clientDepsToBdDeps(resp.Dependencies), nil
}

// --- Mutations ---

func (a *fleetClientAdapter) ClaimIssue(ctx context.Context, id string) error {
	_, err := a.client.ClaimIssue(ctx, a.workspace, id, 0)
	return err
}

func (a *fleetClientAdapter) CloseIssue(ctx context.Context, id, reason string) error {
	return a.client.BatchCloseIssues(ctx, a.workspace, &client.BatchCloseIssuesRequest{
		IssueIDs: []string{id},
		Reason:   reason,
	})
}

func (a *fleetClientAdapter) ReopenIssue(ctx context.Context, id string) error {
	return a.client.ReopenIssue(ctx, a.workspace, id)
}

func (a *fleetClientAdapter) DeferIssue(ctx context.Context, id string, until time.Time) error {
	return a.client.DeferIssue(ctx, a.workspace, id, until)
}

func (a *fleetClientAdapter) AssignIssue(ctx context.Context, id, assignee string) error {
	// Fleet-db has no standalone AssignIssue; ClaimIssue sets assignee to the actor.
	a.logger.Warn("fleet-db: AssignIssue uses ClaimIssue (assignee param used as actor context only)",
		"issue", id, "assignee", assignee)
	_, err := a.client.ClaimIssue(ctx, a.workspace, id, 0)
	return err
}

func (a *fleetClientAdapter) UpdateFields(ctx context.Context, id string, fields map[string]*string) error {
	req := &client.UpdateIssueRequest{}
	if v, ok := fields["title"]; ok {
		req.Title = v
	}
	if v, ok := fields["description"]; ok {
		req.Description = v
	}
	if v, ok := fields["design"]; ok {
		req.Design = v
	}
	if v, ok := fields["notes"]; ok {
		req.Notes = v
	}
	_, err := a.client.UpdateIssue(ctx, a.workspace, id, req)
	return err
}

// --- Type conversion functions ---

func clientIssueToBdIssue(issue *client.Issue) BdIssue {
	return BdIssue{
		ID:        issue.ID,
		Title:     issue.Title,
		Status:    string(issue.Status),
		Priority:  issue.Priority,
		IssueType: string(issue.Type),
		Design:    issue.Design,
		Assignee:  issue.Assignee,
		Labels:    issue.Labels,
		// Dependencies: populated separately by hydration in fleetDBBackend
	}
}

func clientIssuesToBdIssues(issues []*client.Issue) []BdIssue {
	result := make([]BdIssue, 0, len(issues))
	for _, issue := range issues {
		if issue == nil {
			continue
		}
		result = append(result, clientIssueToBdIssue(issue))
	}
	return result
}

func clientDepToBdDep(dep *client.Dependency) Dependency {
	return Dependency{
		IssueID:     dep.IssueID,
		DependsOnID: dep.DependsOnID,
		Type:        string(dep.Type),
		CreatedAt:   dep.CreatedAt.Format(time.RFC3339),
		CreatedBy:   dep.CreatedBy,
	}
}

func clientDepsToBdDeps(deps []*client.Dependency) []Dependency {
	result := make([]Dependency, 0, len(deps))
	for _, dep := range deps {
		if dep == nil {
			continue
		}
		result = append(result, clientDepToBdDep(dep))
	}
	return result
}

func countResponseToBdStats(resp *client.CountIssuesResponse) BdStats {
	g := resp.Groups
	return BdStats{
		Summary: struct {
			TotalIssues      int `json:"total_issues"`
			OpenIssues       int `json:"open_issues"`
			ClosedIssues     int `json:"closed_issues"`
			InProgressIssues int `json:"in_progress_issues"`
			BlockedIssues    int `json:"blocked_issues"`
			DeferredIssues   int `json:"deferred_issues"`
			TombstoneIssues  int `json:"tombstone_issues"`
			PinnedIssues     int `json:"pinned_issues"`
		}{
			TotalIssues:      int(resp.Total),
			OpenIssues:       int(g["open"]),
			ClosedIssues:     int(g["closed"]),
			InProgressIssues: int(g["in_progress"]),
			BlockedIssues:    int(g["blocked"]),
			DeferredIssues:   int(g["deferred"]),
			TombstoneIssues:  int(g["tombstone"]),
			PinnedIssues:     int(g["pinned"]),
		},
	}
}

func formatIssueText(issue *client.Issue) string {
	text := fmt.Sprintf("# %s: %s\nStatus: %s  Priority: %d  Type: %s\n",
		issue.ID, issue.Title, issue.Status, issue.Priority, issue.Type)
	if issue.Assignee != "" {
		text += fmt.Sprintf("Assignee: %s\n", issue.Assignee)
	}
	if issue.Description != "" {
		text += "---\n" + issue.Description + "\n"
	}
	return text
}
