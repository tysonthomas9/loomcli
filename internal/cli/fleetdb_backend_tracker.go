// fleetdb_backend_tracker.go — IssueTracker typed methods for fleetDBBackend.
// These methods provide direct typed access to the RPC client.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// Compile-time interface check.
var _ IssueTracker = (*fleetDBBackend)(nil)

func (b *fleetDBBackend) BackendName() string { return "fleet-db" }

func (b *fleetDBBackend) Ready(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
	rpcArgs := &rpc.ReadyArgs{Limit: opts.Limit, ParentID: opts.ParentID, Labels: opts.Labels, SourceRepos: opts.SourceRepos}
	resp, err := b.client.Ready(rpcArgs)
	if err != nil {
		return nil, fmt.Errorf("ready: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("ready: %s", resp.Error)
	}
	var issues []*types.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, fmt.Errorf("ready: unmarshal: %w", err)
	}
	return issuesToBdIssues(issues), nil
}

func (b *fleetDBBackend) List(_ context.Context, opts ListOpts) ([]BdIssue, error) {
	rpcArgs := &rpc.ListArgs{Status: opts.Status, Assignee: opts.Assignee, IssueType: opts.Type, ParentID: opts.ParentID, Limit: opts.Limit}
	resp, err := b.client.List(rpcArgs)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("list: %s", resp.Error)
	}
	var issues []*types.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, fmt.Errorf("list: unmarshal: %w", err)
	}
	return issuesToBdIssues(issues), nil
}

func (b *fleetDBBackend) Blocked(_ context.Context) ([]BdIssue, error) {
	resp, err := b.client.Blocked(&rpc.BlockedArgs{})
	if err != nil {
		return nil, fmt.Errorf("blocked: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("blocked: %s", resp.Error)
	}
	var issues []*types.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, fmt.Errorf("blocked: unmarshal: %w", err)
	}
	return issuesToBdIssues(issues), nil
}

func (b *fleetDBBackend) Stats(_ context.Context) (*BdStats, error) {
	resp, err := b.client.Stats()
	if err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("stats: %s", resp.Error)
	}
	var stats types.Statistics
	if err := json.Unmarshal(resp.Data, &stats); err != nil {
		return nil, fmt.Errorf("stats: unmarshal: %w", err)
	}
	result := statisticsToBdStats(&stats)
	return &result, nil
}

func (b *fleetDBBackend) GetIssue(_ context.Context, id string) (*BdIssue, error) {
	resp, err := b.client.Show(&rpc.ShowArgs{ID: id})
	if err != nil {
		return nil, fmt.Errorf("show: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("show: %s", resp.Error)
	}
	var details types.IssueDetails
	if err := json.Unmarshal(resp.Data, &details); err != nil {
		return nil, fmt.Errorf("show: unmarshal: %w", err)
	}
	result := issueDetailsToBdIssue(&details)
	return &result, nil
}

func (b *fleetDBBackend) GetIssueText(_ context.Context, id string) (string, error) {
	resp, err := b.client.Show(&rpc.ShowArgs{ID: id})
	if err != nil {
		return "", fmt.Errorf("show: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("show: %s", resp.Error)
	}
	var details types.IssueDetails
	if err := json.Unmarshal(resp.Data, &details); err != nil {
		return "", fmt.Errorf("show: unmarshal: %w", err)
	}
	return formatIssueDetailsText(&details), nil
}

// formatIssueDetailsText formats IssueDetails as concise human-readable text
// for LLM consumption (used by recover_helpers.go completion analysis).
func formatIssueDetailsText(d *types.IssueDetails) string {
	var b strings.Builder
	b.WriteString("Title: ")
	b.WriteString(d.Title)
	b.WriteByte('\n')

	if d.Status != "" {
		b.WriteString("Status: ")
		b.WriteString(string(d.Status))
		b.WriteByte('\n')
	}

	b.WriteString("Priority: P")
	b.WriteString(fmt.Sprintf("%d", d.Priority))
	b.WriteByte('\n')

	if d.IssueType != "" {
		b.WriteString("Type: ")
		b.WriteString(string(d.IssueType))
		b.WriteByte('\n')
	}

	if d.Description != "" {
		desc := d.Description
		const maxDesc = 2000
		if len(desc) > maxDesc {
			desc = desc[:maxDesc] + "..."
		}
		b.WriteString("Description:\n")
		b.WriteString(desc)
		b.WriteByte('\n')
	}

	if d.Design != "" {
		design := d.Design
		const maxDesign = 1000
		if len(design) > maxDesign {
			design = design[:maxDesign] + "..."
		}
		b.WriteString("Design:\n")
		b.WriteString(design)
		b.WriteByte('\n')
	}

	return b.String()
}

func (b *fleetDBBackend) UpdateIssue(_ context.Context, id string, opts UpdateOpts) error {
	rpcArgs := &rpc.UpdateArgs{ID: id, Claim: opts.Claim}
	if opts.Status != "" {
		rpcArgs.Status = &opts.Status
	}
	if opts.Assignee != nil {
		rpcArgs.Assignee = opts.Assignee
	}
	if opts.Design != "" {
		rpcArgs.Design = &opts.Design
	}
	resp, err := b.client.Update(rpcArgs)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("update: %s", resp.Error)
	}
	return nil
}

func (b *fleetDBBackend) UpdateExternalRef(_ context.Context, id, ref string) error {
	rpcArgs := &rpc.UpdateArgs{ID: id, ExternalRef: &ref}
	resp, err := b.client.Update(rpcArgs)
	if err != nil {
		return fmt.Errorf("update external ref: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("update external ref: %s", resp.Error)
	}
	return nil
}

func (b *fleetDBBackend) CloseIssue(_ context.Context, id, reason string) error {
	resp, err := b.client.CloseIssue(&rpc.CloseArgs{ID: id, Reason: reason})
	if err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("close: %s", resp.Error)
	}
	return nil
}

// issuesToBdIssues converts a slice of types.Issue pointers to []BdIssue.
func issuesToBdIssues(issues []*types.Issue) []BdIssue {
	result := make([]BdIssue, 0, len(issues))
	for _, issue := range issues {
		result = append(result, issueToBdIssue(issue))
	}
	return result
}
