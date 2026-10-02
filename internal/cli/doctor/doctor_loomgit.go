package doctor

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit/mirror"
	loomstatus "github.com/tysonthomas9/loomcli/internal/loomgit/status"
)

func checkLoomGitInventory(ctx context.Context, integrity bool) CheckResult {
	snapshot, err := loomstatus.Scan(ctx, integrity)
	if err != nil {
		return CheckResult{Name: "loom_git_inventory", Status: StatusWarn, Summary: "Loom Git inventory unavailable", Detail: err.Error()}
	}
	result := CheckResult{Name: "loom_git_inventory", Status: StatusPass, Summary: fmt.Sprintf("Loom Git inventory: %d objects, %d changed files", len(snapshot.Entries), snapshot.ChangedTotal)}
	var details []string
	for _, entry := range snapshot.Entries {
		line := fmt.Sprintf("%s %s/%s %s: %s", entry.Kind, entry.Workspace, entry.Repo, entry.ID, entry.State)
		if entry.Path != "" {
			line += " at " + entry.Path
		}
		if entry.Drift != "" {
			line += fmt.Sprintf("; %s (behind %d, ahead %d)", entry.Drift, entry.Behind, entry.Ahead)
		}
		if entry.BaseDrift != "" {
			line += fmt.Sprintf("; base %s (behind %d, ahead %d)", entry.BaseDrift, entry.BaseBehind, entry.BaseAhead)
		}
		if entry.Reason != "" {
			line += "; " + entry.Reason
		}
		if entry.NextAction != "" {
			line += "; next: " + entry.NextAction
		}
		details = append(details, line)
		if entry.State == "integrity_missing" || entry.State == "integrity_unverified" || entry.State == "missing_checkout" || entry.State == "unowned" || entry.State == "not_synced" || entry.Drift == "diverged" {
			result.Status = StatusWarn
		}
	}
	remoteIssues, remoteErr := mirror.RemoteIntegrity(ctx)
	if remoteErr != nil {
		result.Status = StatusWarn
		details = append(details, "Provider integrity unavailable: "+remoteErr.Error())
	}
	for _, issue := range remoteIssues {
		result.Status = StatusWarn
		details = append(details, fmt.Sprintf("%s %s expected %s: %s", issue.State, issue.Ref, issue.SHA, issue.Reason))
	}
	if snapshot.Truncated {
		details = append(details, fmt.Sprintf("Changed file list truncated at %d of %d entries", len(snapshot.Changed), snapshot.ChangedTotal))
	}
	result.Detail = strings.Join(details, "\n")
	return result
}
