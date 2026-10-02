package stackpublish

import (
	"context"
	"fmt"
	"strings"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

// Reconciler reads live PR health for a legacy stackstore stack.
type Reconciler struct {
	Store stackstore.Store
	Forge Forge
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
	nodes, err := r.Store.ListNodes(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	ordered, err := sl.Ordered(nodes)
	if err != nil {
		return nil, fmt.Errorf("invalid lineage: %w", err)
	}
	report := &StatusReport{StackID: id}
	nextSet := sl.NextToMergeUnits(ordered)

	var statuses map[string]PRStatus
	if strings.TrimSpace(repoPath) != "" {
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
