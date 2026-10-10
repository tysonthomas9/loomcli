package doctor

import (
	"context"
	"fmt"
	"os"
	"strings"

	loomretention "github.com/tysonthomas9/loomcli/internal/loomgit/retention"
)

// checkLoomGitRetention reports the task copies and refs retention can clean
// up; with --fix it removes them (this was loom retention-sweep, S3).
func checkLoomGitRetention(ctx context.Context, path string, fix bool) CheckResult {
	result := CheckResult{Name: "loom_git_retention", Status: StatusPass, Summary: "Loom Git retention: nothing to clean up"}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		result.Summary = "Loom Git retention: no records"
		return result
	} else if err != nil {
		return CheckResult{Name: result.Name, Status: StatusWarn, Summary: "Loom Git retention unavailable", Detail: err.Error()}
	}
	results, err := loomretention.RunAt(ctx, path, fix)
	var details []string
	pending := 0
	for _, item := range results {
		details = append(details, fmt.Sprintf("%s %s %s %s: %s", item.Action, item.Workspace, item.Change, item.Path, item.Reason))
		if !fix && item.Action != "keep" && item.Action != "removed" {
			pending++
		}
	}
	if pending > 0 {
		result.Status = StatusWarn
		result.Summary = fmt.Sprintf("Loom Git retention: %d item(s) can be cleaned up; run loom doctor --fix", pending)
	} else if fix && len(results) > 0 {
		result.Summary = "Loom Git retention: cleaned up"
	}
	if err != nil {
		result.Status = StatusWarn
		details = append(details, "Retention sweep failed: "+err.Error())
	}
	result.Detail = strings.Join(details, "\n")
	return result
}
