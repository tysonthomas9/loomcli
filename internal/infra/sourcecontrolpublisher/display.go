package stackpublish

import (
	"context"
	"fmt"
	"strings"

	sl "github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol/stacklineage"
)

// Stack-listing section markers in a PR body. The reconciler owns the content
// between them and preserves everything outside (human-edited description).
const (
	stackMarkStart = "<!-- loom-stack:start -->"
	stackMarkEnd   = "<!-- loom-stack:end -->"
)

// renderStackListing renders the stack as a checklist for a PR body, marking the
// current unit. Only units with a live PR are listed (merged/empty are omitted).
func renderStackListing(ordered []sl.Node, live map[string]PR, current string, id sl.StackID) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📚 **Loom stack** `%s`\n", id)
	for _, n := range ordered {
		pr, ok := live[n.TaskID]
		if !ok {
			continue
		}
		marker := "-"
		if n.TaskID == current {
			marker = "- 👉"
		}
		fmt.Fprintf(&b, "%s #%d `%s`\n", marker, pr.Number, n.OutputBranch)
	}
	return strings.TrimRight(b.String(), "\n")
}

// withStackSection returns body with the loom-stack section set to listing,
// replacing an existing section in place or appending one, preserving the rest.
func withStackSection(body, listing string) string {
	section := stackMarkStart + "\n" + listing + "\n" + stackMarkEnd
	si := strings.Index(body, stackMarkStart)
	ei := strings.Index(body, stackMarkEnd)
	if si >= 0 && ei > si {
		return body[:si] + section + body[ei+len(stackMarkEnd):]
	}
	if strings.TrimSpace(body) == "" {
		return section
	}
	return body + "\n\n" + section
}

// StatusRow is one unit's row in a status report, enriched with live PR health
// when available. Consumed by `loom stack status` and (via --json) any UI.
type StatusRow struct {
	TaskID       string       `json:"taskId"`
	State        sl.NodeState `json:"state"`
	OutputBranch string       `json:"outputBranch"`
	PRNumber     int          `json:"prNumber,omitempty"`
	PRURL        string       `json:"prUrl,omitempty"`
	Checks       string       `json:"checks,omitempty"`
	Review       string       `json:"review,omitempty"`
	Mergeable    string       `json:"mergeable,omitempty"`
	NextToMerge  bool         `json:"nextToMerge,omitempty"`
}

// StatusReport is the enriched status of a stack.
type StatusReport struct {
	StackID sl.StackID  `json:"stackId"`
	Live    bool        `json:"live"` // whether live PR health was fetched
	Rows    []StatusRow `json:"rows"`
}

// StackStatus returns the stack's units enriched with live PR health (checks /
// review / mergeable) and a next-to-merge marker. repoPath provides the owner/repo;
// pass "" to skip the live fetch and return local state only.
func (r *Reconciler) StackStatus(ctx context.Context, ws string, id sl.StackID, repoPath string) (*StatusReport, error) {
	nodeProjections, err := r.Stacks.ListStackNodes(ctx, ws, string(id))
	if err != nil {
		return nil, err
	}
	nodes := legacyStackNodes(nodeProjections)
	ordered, err := sl.Ordered(nodes)
	if err != nil {
		return nil, fmt.Errorf("invalid lineage: %w", err)
	}
	report := &StatusReport{StackID: id}
	nextSet := sl.NextToMergeUnits(ordered)

	var statuses map[string]PRStatus
	if strings.TrimSpace(repoPath) != "" && forgeSupportsPullRequests(r.Forge) {
		owner, repo, rerr := repoSlug(ctx, repoPath)
		if rerr != nil {
			return nil, rerr
		}
		statuses, err = r.Forge.PRStatuses(ctx, owner, repo, sl.StackBranchPrefix(id))
		if err != nil {
			return nil, err
		}
		report.Live = true
	}

	for _, n := range ordered {
		row := StatusRow{
			TaskID: n.TaskID, State: n.State, OutputBranch: n.OutputBranch,
			PRNumber: n.PRNumber, PRURL: n.PRURL, NextToMerge: nextSet[n.TaskID],
		}
		if st, ok := statuses[n.OutputBranch]; ok {
			row.Checks, row.Review, row.Mergeable = st.Checks, st.Review, st.Mergeable
			if row.PRNumber == 0 {
				row.PRNumber = st.Number
			}
		}
		report.Rows = append(report.Rows, row)
	}
	return report, nil
}
