package doctor

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent/lead"
)

// leadPersonaSuppression is the suppression probe, indirected so the check's
// verdicts can be exercised without standing up a workspace store.
var leadPersonaSuppression = lead.PersonaSuppression

// checkLeadSafetyDrift reports whether a lead whose argv persona is suppressed
// still has the CURRENT multi-agent safety block in the ambient file the
// harness will actually read.
//
// It SKIPS - returns the zero CheckResult, the way checkOrphanedTranscripts
// does when its store is unavailable - for a lead whose persona is not
// suppressed. That is not the same as a pass: with the persona on argv the
// safety block is rendered per run and there is nothing that can go stale, so
// reporting a green line would only claim a guarantee nobody asked for.
func checkLeadSafetyDrift() CheckResult {
	ctx := context.Background()
	reason, suppressed := leadPersonaSuppression(ctx)
	if !suppressed {
		return CheckResult{} // skip — the persona is on argv, nothing can go stale
	}

	workDir, dedicated, err := lead.ResolveWorkdir(ctx)
	if err != nil {
		return CheckResult{
			Name:    "lead_safety_drift",
			Status:  StatusWarn,
			Summary: "could not resolve the lead workdir to check for safety-block drift",
			Detail:  err.Error(),
		}
	}
	if err := lead.CheckAmbientSafetyBlock(cli.GetBackendName(), workDir, dedicated, reason); err != nil {
		return CheckResult{
			Name:    "lead_safety_drift",
			Status:  StatusFail,
			Summary: "lead persona is suppressed but the ambient safety block is stale",
			Detail:  err.Error(),
		}
	}
	return CheckResult{
		Name:    "lead_safety_drift",
		Status:  StatusPass,
		Summary: "lead ambient safety block is current",
	}
}
