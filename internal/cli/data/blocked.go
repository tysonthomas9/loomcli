package data

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

var (
	blockedLimit  int
	blockedType   string
	blockedParent string
)

var blockedCmd = &cobra.Command{
	Use:   "blocked",
	Short: "List blocked issues — blocked by an unclosed blocker dependency, or with status=blocked",
	Long: `List blocked issues.

Two kinds of blockage are shown, merged into one view:

  * dependency-blocked — the issue has an unclosed "blocks" dependency (or
    inherits one from its parent). These keep their own status, usually open.
  * status-blocked — the issue's own status field is "blocked" (parked), which
    the dependency graph knows nothing about.

The STATUS column discriminates the two: "blocked" means the issue is parked,
anything else means it is waiting on a blocker. An issue that is both appears
once, as its dependency-blocked entry.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		ib, err := getIssueBackend(ctx)
		if err != nil {
			return err
		}
		items, err := fetchBlockedIssues(ctx, ib)
		if err != nil {
			return err
		}
		return printIssueList(os.Stdout, items, outputFormat)
	},
}

// blockedQuerier is the slice of backend.IssueBackend this command needs. It is
// declared here rather than taking the full interface so the merge logic can be
// exercised with a small in-package stub: cli/data may not import cli/clitest
// (the data-isolation depguard rule).
type blockedQuerier interface {
	Blocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error)
	List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error)
}

// fetchBlockedIssues unions the backend's canonical dependency-blocked view
// with the issues whose own status field is "blocked". The canonical view is a
// pure dependency-edge query server-side and never consults issues.status, so
// parked issues are otherwise invisible to this command.
//
// TEMPORARY COMPATIBILITY SHIM. docs/product/lead-agent-epic-runner-spec.md
// ("FleetDB State Contract Guardrail") requires FleetDB's /issues/blocked to
// include status-blocked issues itself, and forbids a permanent client-side
// union. Today it does not: fleet-db's blockedCTE
// (internal/storage/postgres/query.go) computes blockage purely from
// dependency edges plus parent propagation. Remove this union — and the List
// call below — once a fleet-db release makes /issues/blocked canonical per
// that contract; the command then goes back to a single Blocked call.
func fetchBlockedIssues(ctx context.Context, ib blockedQuerier) ([]backend.IssueData, error) {
	depBlocked, err := ib.Blocked(ctx, backend.BlockedOpts{
		Limit:    blockedLimit,
		Type:     blockedType,
		ParentID: blockedParent,
	})
	if err != nil {
		return nil, err
	}
	statusBlocked, err := ib.List(ctx, backend.ListOpts{
		Status:    "blocked",
		IssueType: blockedType,
		ParentID:  blockedParent,
		Limit:     blockedLimit,
	})
	if err != nil {
		return nil, err
	}
	return mergeBlocked(depBlocked, statusBlocked, blockedLimit), nil
}

// mergeBlocked concatenates the two blocked views, de-duplicating by ID.
// Dependency-blocked entries come first and win a tie: they carry BlockedBy /
// BlockedByCount metadata the plain list result does not. The limit is
// re-applied after the merge so --limit keeps meaning "at most N rows printed".
func mergeBlocked(dep, status []backend.IssueData, limit int) []backend.IssueData {
	out := make([]backend.IssueData, 0, len(dep)+len(status))
	seen := make(map[string]struct{}, len(dep)+len(status))
	for _, group := range [][]backend.IssueData{dep, status} {
		for _, issue := range group {
			if _, dup := seen[issue.ID]; dup {
				continue
			}
			seen[issue.ID] = struct{}{}
			out = append(out, issue)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func init() {
	blockedCmd.Flags().IntVar(&blockedLimit, "limit", 0, "Maximum number of results (0 = server default)")
	blockedCmd.Flags().StringVar(&blockedType, "type", "", "Filter by issue type")
	blockedCmd.Flags().StringVar(&blockedParent, "parent", "", "Filter by parent issue ID")
}
