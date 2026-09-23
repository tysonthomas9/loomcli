package doctor

import (
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// checkPlannerHostSubmit verifies plan-role agents carry host-owned
// write_design + set_status review hooks. Legacy agents without them fail
// closed at spawn on hard read-only backends; doctor surfaces the exact
// agentdef remediation before paid execution.
func checkPlannerHostSubmit() CheckResult {
	runtimeDir := cli.GetWorkspaceRuntimeDir()
	dc, err := cfgpkg.LoadDaemonConfig(runtimeDir)
	if err != nil {
		return CheckResult{
			Name:    "planner_host_submit",
			Status:  StatusWarn,
			Summary: "could not load daemon profile to check planner hooks",
			Detail:  err.Error(),
		}
	}
	return evaluatePlannerHostSubmit(dc)
}

func evaluatePlannerHostSubmit(dc *cfgpkg.DaemonConfig) CheckResult {
	if dc == nil || len(dc.Agents) == 0 {
		return CheckResult{
			Name:    "planner_host_submit",
			Status:  StatusPass,
			Summary: "no agents configured",
		}
	}
	var missing []string
	planCount := 0
	for _, a := range dc.Agents {
		if a.Role != "plan" {
			continue
		}
		planCount++
		if backends.HasHostDesignSubmit(a.Hooks) && backends.HasHostStatusReview(a.Hooks) {
			continue
		}
		missing = append(missing, backends.PlanHostSubmitRemediation(a.Worktree))
	}
	switch {
	case planCount == 0:
		return CheckResult{Name: "planner_host_submit", Status: StatusPass, Summary: "no plan agents"}
	case len(missing) == 0:
		return CheckResult{
			Name:    "planner_host_submit",
			Status:  StatusPass,
			Summary: fmt.Sprintf("%d plan agent(s) have host design submit hooks", planCount),
		}
	default:
		return CheckResult{
			Name:    "planner_host_submit",
			Status:  StatusFail,
			Summary: fmt.Sprintf("%d plan agent(s) missing host design submit hooks", len(missing)),
			Detail: strings.Join(missing, "\n") +
				"\nHard read-only backends (claude/codex) refuse spawn without these hooks.",
		}
	}
}
