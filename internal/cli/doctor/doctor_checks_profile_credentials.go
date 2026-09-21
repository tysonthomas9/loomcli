package doctor

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/cli"
)

// credentialFault is one profile whose harness-owned credential file did not
// verify, kept with the error so the report can bucket by severity rather than
// by message text.
type credentialFault struct {
	profile agentprofile.Profile
	err     error
}

// checkProfileCredentials inspects the CONTENT of each provisioned profile's
// harness-owned credential file.
//
// It is a separate check from checkAgentProfiles rather than another bucket
// inside it, for three reasons. The credential fault is independent of the
// manifest and the version pin — a profile can be both drifted and hollow, and
// the operator needs both lines. The watcher escalates per check NAME, so a
// distinct name gives it something to escalate on. And checkAgentProfiles
// already warns when `claude --version` yields nothing, which must not mask a
// credential fault underneath it.
//
// Severity is split by confidence, deliberately: only a hollow file fails,
// because only a hollow file is unambiguous. See agentprofile.VerifyCredentials.
//
// --fix does not participate. Deleting a credential is irreversible content
// mutation, and profile content routes to the operator's provisioner — the same
// boundary the manifest checks draw.
func checkProfileCredentials() CheckResult {
	projectDir := cli.GetWorkspaceRuntimeDir()
	if projectDir == "" {
		return CheckResult{}
	}
	profiles, err := agentprofile.List(projectDir)
	if err != nil {
		return CheckResult{
			Name:    "agent_profile_credentials",
			Status:  StatusWarn,
			Summary: "could not enumerate agent profiles",
			Detail:  err.Error(),
		}
	}
	// A fleet with no profiles sees no new output at all.
	if len(profiles) == 0 {
		return CheckResult{}
	}

	var hollow, unreadable []credentialFault
	for _, p := range profiles {
		err := agentprofile.VerifyCredentials(p.Dir, p.Harness)
		switch {
		case err == nil:
		case errors.Is(err, agentprofile.ErrCredentialsHollow):
			hollow = append(hollow, credentialFault{profile: p, err: err})
		default:
			unreadable = append(unreadable, credentialFault{profile: p, err: err})
		}
	}

	total := len(profiles)
	switch {
	case len(hollow) > 0:
		detail := credentialFaultLines(hollow)
		if doctorFix {
			detail = append(detail, "--fix does not remove credential files")
		}
		return CheckResult{
			Name:    "agent_profile_credentials",
			Status:  StatusFail,
			Summary: fmt.Sprintf("%d of %d agent profile(s) carry an empty harness credential", len(hollow), total),
			Detail:  strings.Join(detail, "\n"),
		}
	case len(unreadable) > 0:
		return CheckResult{
			Name:    "agent_profile_credentials",
			Status:  StatusWarn,
			Summary: fmt.Sprintf("%d of %d agent profile(s) have an unreadable harness credential", len(unreadable), total),
			Detail:  strings.Join(credentialFaultLines(unreadable), "\n"),
		}
	default:
		// "or none" is not hedging: an absent file is a first-class healthy
		// state here, and the summary must not imply one was found.
		return CheckResult{
			Name:    "agent_profile_credentials",
			Status:  StatusPass,
			Summary: fmt.Sprintf("%d agent profile(s) have a usable harness credential or none", total),
		}
	}
}

// credentialFaultLines renders one block per failing profile — the agent, its
// directory, why it is a fault, and the exact repair — sorted by (agent,
// harness), the same ordering rule as faultLines. Healthy profiles never
// appear, and no token bytes do either: the reason is written here, not taken
// from the file.
func credentialFaultLines(faults []credentialFault) []string {
	sorted := append([]credentialFault{}, faults...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].profile.Agent != sorted[j].profile.Agent {
			return sorted[i].profile.Agent < sorted[j].profile.Agent
		}
		return sorted[i].profile.Harness < sorted[j].profile.Harness
	})

	var out []string
	for _, f := range sorted {
		out = append(out, fmt.Sprintf("%s  %s", f.profile.Agent, displayDir(f.profile.Dir)))
		out = append(out, credentialReason(f)...)
		out = append(out, "    repair: rm "+displayDir(credentialsPathOf(f.profile))+
			"   (restores the env-token path; --fix will not touch this)")
	}
	return out
}

// credentialReason states the fault in the operator's terms. The hollow case
// spells out the SHADOWING, because "empty credential" alone does not explain
// why a profile with a perfectly good injected token produced nothing.
func credentialReason(f credentialFault) []string {
	if errors.Is(f.err, agentprofile.ErrCredentialsHollow) {
		return []string{
			"    " + agentprofile.CredentialsName + " has no access token; it SHADOWS the CLAUDE_CODE_OAUTH_TOKEN",
			"    this profile injects, so the harness blocks on an interactive login prompt",
			"    and the run is reaped on the turn deadline",
		}
	}
	if errors.Is(f.err, agentprofile.ErrCredentialsUnknownShape) {
		return []string{"    " + agentprofile.CredentialsName + " is valid JSON with no recognizable OAuth object"}
	}
	return []string{"    " + agentprofile.CredentialsName + " could not be read or parsed (retried once)"}
}

func credentialsPathOf(p agentprofile.Profile) string {
	if path := agentprofile.CredentialsPath(p.Dir, p.Harness); path != "" {
		return path
	}
	return p.Dir
}
