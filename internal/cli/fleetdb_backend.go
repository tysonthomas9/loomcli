// Package cli — fleetDBBackend wraps an RPC client to communicate with a running
// daemon instance. It implements RunCommand (the IssueBackend interface from
// issue_backend.go) to translate bd-style CLI arguments into RPC calls, enabling
// monitor.go callers to switch from shelling out to the bd CLI to making RPC calls
// without changing any calling code.
//
// This is distinct from Backend/StreamingBackend (backend.go, backend_capabilities.go)
// which handle AI agent invocation. fleetDBBackend handles issue data operations only.
package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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

// dispatchFunc handles a dispatched command, receiving parsed flags and positional args.
type dispatchFunc func(flags map[string]string, positional []string) (string, error)

// fleetDBBackend wraps an RPC client and workspace string to implement the
// IssueBackend interface (RunCommand dispatch table).
type fleetDBBackend struct {
	client    fleetDBClient
	workspace string
	dispatch  map[string]dispatchFunc
}

// newFleetDBBackend creates a new fleetDBBackend with the given RPC client and workspace.
func newFleetDBBackend(client fleetDBClient, workspace string) *fleetDBBackend { //nolint:unparam // workspace will vary when wired by task .6
	b := &fleetDBBackend{
		client:    client,
		workspace: workspace,
	}
	b.dispatch = map[string]dispatchFunc{
		"ready":   b.handleReady,
		"list":    b.handleList,
		"blocked": b.handleBlocked,
		"stats":   b.handleStats,
		"show":    b.handleShow,
		"update":  b.handleUpdate,
		"close":   b.handleClose,
		"sync":    b.handleSync,
	}
	return b
}

// RunCommand parses bd-style CLI args and dispatches to the corresponding RPC method.
// The dir parameter is ignored — the RPC client already knows how to reach the daemon.
// This satisfies the IssueBackend interface.
func (b *fleetDBBackend) RunCommand(_ string, args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("no command specified")
	}

	cmd := args[0]
	handler, ok := b.dispatch[cmd]
	if !ok {
		return "", fmt.Errorf("unknown command: %s", cmd)
	}

	flags, positional := parseArgs(args[1:])
	return handler(flags, positional)
}

// parseArgs performs a simple linear scan of args, supporting:
//   - --flag=value
//   - --flag value
//   - bare --flag (boolean true)
//   - positional args (anything not starting with --)
func parseArgs(args []string) (flags map[string]string, positional []string) {
	flags = make(map[string]string)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			positional = append(positional, arg)
			continue
		}
		// Strip leading --
		key := arg[2:]

		// Handle --flag=value
		if eqIdx := strings.IndexByte(key, '='); eqIdx >= 0 {
			flags[key[:eqIdx]] = key[eqIdx+1:]
			continue
		}

		// Handle --flag value or bare --flag
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			flags[key] = args[i+1]
			i++
		} else {
			flags[key] = "true"
		}
	}
	return flags, positional
}

// --- dispatch handlers ---

func (b *fleetDBBackend) handleReady(flags map[string]string, _ []string) (string, error) {
	rpcArgs := &rpc.ReadyArgs{}
	if v, ok := flags["limit"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			rpcArgs.Limit = n
		}
	}
	if v, ok := flags["assignee"]; ok {
		rpcArgs.Assignee = v
	}
	if v, ok := flags["parent"]; ok {
		rpcArgs.ParentID = v
	}

	resp, err := b.client.Ready(rpcArgs)
	if err != nil {
		return "", fmt.Errorf("ready: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("ready: %s", resp.Error)
	}

	var issues []*types.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return "", fmt.Errorf("ready: unmarshal response: %w", err)
	}

	return marshalBdIssues(issues)
}

func (b *fleetDBBackend) handleList(flags map[string]string, _ []string) (string, error) {
	rpcArgs := &rpc.ListArgs{}
	if v, ok := flags["status"]; ok {
		rpcArgs.Status = v
	}
	if v, ok := flags["assignee"]; ok {
		rpcArgs.Assignee = v
	}
	if v, ok := flags["limit"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			rpcArgs.Limit = n
		}
	}
	if v, ok := flags["type"]; ok {
		rpcArgs.IssueType = v
	}

	resp, err := b.client.List(rpcArgs)
	if err != nil {
		return "", fmt.Errorf("list: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("list: %s", resp.Error)
	}

	var issues []*types.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return "", fmt.Errorf("list: unmarshal response: %w", err)
	}

	return marshalBdIssues(issues)
}

func (b *fleetDBBackend) handleBlocked(flags map[string]string, _ []string) (string, error) {
	rpcArgs := &rpc.BlockedArgs{}
	if v, ok := flags["limit"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			rpcArgs.Limit = n
		}
	}
	if v, ok := flags["assignee"]; ok {
		rpcArgs.Assignee = v
	}

	resp, err := b.client.Blocked(rpcArgs)
	if err != nil {
		return "", fmt.Errorf("blocked: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("blocked: %s", resp.Error)
	}

	var issues []*types.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return "", fmt.Errorf("blocked: unmarshal response: %w", err)
	}

	return marshalBdIssues(issues)
}

func (b *fleetDBBackend) handleStats(_ map[string]string, _ []string) (string, error) {
	resp, err := b.client.Stats()
	if err != nil {
		return "", fmt.Errorf("stats: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("stats: %s", resp.Error)
	}

	var stats types.Statistics
	if err := json.Unmarshal(resp.Data, &stats); err != nil {
		return "", fmt.Errorf("stats: unmarshal response: %w", err)
	}

	bdStats := statisticsToBdStats(&stats)
	out, err := json.Marshal(bdStats)
	if err != nil {
		return "", fmt.Errorf("stats: marshal response: %w", err)
	}
	return string(out), nil
}

func (b *fleetDBBackend) handleShow(_ map[string]string, positional []string) (string, error) {
	if len(positional) == 0 {
		return "", fmt.Errorf("show requires an issue ID")
	}

	resp, err := b.client.Show(&rpc.ShowArgs{ID: positional[0]})
	if err != nil {
		return "", fmt.Errorf("show: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("show: %s", resp.Error)
	}

	var details types.IssueDetails
	if err := json.Unmarshal(resp.Data, &details); err != nil {
		return "", fmt.Errorf("show: unmarshal response: %w", err)
	}

	bdIssue := issueDetailsToBdIssue(&details)
	out, err := json.Marshal(bdIssue)
	if err != nil {
		return "", fmt.Errorf("show: marshal response: %w", err)
	}
	return string(out), nil
}

func (b *fleetDBBackend) handleUpdate(flags map[string]string, positional []string) (string, error) {
	if len(positional) == 0 {
		return "", fmt.Errorf("update requires an issue ID")
	}

	rpcArgs := &rpc.UpdateArgs{ID: positional[0]}

	if v, ok := flags["status"]; ok {
		rpcArgs.Status = &v
	}
	if v, ok := flags["assignee"]; ok {
		rpcArgs.Assignee = &v
	}
	if v, ok := flags["design"]; ok {
		rpcArgs.Design = &v
	}
	if v, ok := flags["notes"]; ok {
		rpcArgs.Notes = &v
	}
	if v, ok := flags["title"]; ok {
		rpcArgs.Title = &v
	}
	if _, ok := flags["claim"]; ok {
		rpcArgs.Claim = true
	}
	if v, ok := flags["priority"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			rpcArgs.Priority = &n
		}
	}

	resp, err := b.client.Update(rpcArgs)
	if err != nil {
		return "", fmt.Errorf("update: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("update: %s", resp.Error)
	}

	var issue types.Issue
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		return "", fmt.Errorf("update: unmarshal response: %w", err)
	}

	bdIssue := issueToBdIssue(&issue)
	out, err := json.Marshal(bdIssue)
	if err != nil {
		return "", fmt.Errorf("update: marshal response: %w", err)
	}
	return string(out), nil
}

func (b *fleetDBBackend) handleClose(flags map[string]string, positional []string) (string, error) {
	if len(positional) == 0 {
		return "", fmt.Errorf("close requires an issue ID")
	}

	rpcArgs := &rpc.CloseArgs{ID: positional[0]}
	if v, ok := flags["reason"]; ok {
		rpcArgs.Reason = v
	}

	resp, err := b.client.CloseIssue(rpcArgs)
	if err != nil {
		return "", fmt.Errorf("close: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("close: %s", resp.Error)
	}

	var issue types.Issue
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		return "", fmt.Errorf("close: unmarshal response: %w", err)
	}

	bdIssue := issueToBdIssue(&issue)
	out, err := json.Marshal(bdIssue)
	if err != nil {
		return "", fmt.Errorf("close: marshal response: %w", err)
	}
	return string(out), nil
}

func (b *fleetDBBackend) handleSync(_ map[string]string, _ []string) (string, error) {
	// Sync is a no-op for fleet-db — there is no concept of syncing.
	return "", nil
}

// --- type conversion functions ---

// marshalBdIssues converts a slice of types.Issue to []BdIssue JSON.
// Returns "[]" for nil/empty input, never "null".
func marshalBdIssues(issues []*types.Issue) (string, error) {
	bdIssues := make([]BdIssue, 0, len(issues))
	for _, issue := range issues {
		bdIssues = append(bdIssues, issueToBdIssue(issue))
	}
	out, err := json.Marshal(bdIssues)
	if err != nil {
		return "", fmt.Errorf("marshal issues: %w", err)
	}
	return string(out), nil
}

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
