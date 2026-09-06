package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent/lead"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// bindingRow is one harness's verdict. The status travels with the line so the
// check can report the worst outcome without re-parsing its own prose.
type bindingRow struct {
	status CheckStatus
	line   string
}

// checkLeadProfileBinding reports what config root THIS SHELL would hand to a
// harness it launches — the binding, not the profile's content.
//
// checkAgentProfiles walks the profile directory and verifies what is in it.
// Nothing anywhere reported whether the shell about to start a lead is bound
// to one of those profiles at all, which is exactly the blind spot that let
// PUPPET-523 run unnoticed: a config root spelled differently from the one on
// record was classified as "an operator's own", so it was never verified and
// never given its own credential, silently.
//
// Every message says "this shell" rather than "the lead": doctor also runs
// from agent shells and from PM2, where the bound profile is legitimately that
// agent's own.
func checkLeadProfileBinding() CheckResult {
	runtimeDir := cli.GetWorkspaceRuntimeDir()
	if runtimeDir == "" {
		return CheckResult{}
	}
	var rows []bindingRow
	worst := StatusPass
	for _, harness := range supervisor.ProfileHarnesses() {
		row := leadBindingRow(runtimeDir, harness)
		if row.line == "" {
			continue
		}
		if row.status > worst {
			worst = row.status
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return CheckResult{}
	}
	return renderBindingResult(worst, rows)
}

// renderBindingResult folds the per-harness rows into one CheckResult. Only
// StatusFail makes `loom doctor` exit non-zero, and only the relative-root row
// is a failure: it is the one binding `loom lead` now refuses outright.
func renderBindingResult(worst CheckStatus, rows []bindingRow) CheckResult {
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, r.line)
	}
	summary := fmt.Sprintf("harness profile binding: this shell binds %d harness root(s) cleanly", len(rows))
	switch worst {
	case StatusFail:
		summary = "harness profile binding: this shell exports a config root `loom lead` refuses"
	case StatusWarn:
		summary = "harness profile binding: this shell is not bound to a workspace profile as recorded"
	case StatusPass:
	}
	return CheckResult{
		Name:    "lead_profile_binding",
		Status:  worst,
		Summary: summary,
		Detail:  strings.Join(lines, "\n"),
	}
}

// leadBindingRow classifies one harness's inherited config root exactly as
// applyLeadProfile would. The "inside the tree" question goes through the
// lead package's own exported predicate rather than a second copy: two
// answers to that one question is the bug this check exists to surface.
func leadBindingRow(runtimeDir, harness string) bindingRow {
	envVar := supervisor.ProfileEnvVar(harness)
	if envVar == "" {
		return bindingRow{}
	}
	value := os.Getenv(envVar)
	switch {
	case value == "":
		return bindingRow{StatusPass, fmt.Sprintf(
			"%s unset — `loom lead` will resolve and verify its own profile root", envVar)}
	case !filepath.IsAbs(value):
		return bindingRow{StatusFail, fmt.Sprintf(
			"%s=%s is relative; `loom lead` refuses this\n    repair: export an absolute path (%s=$(cd <dir> && pwd)), or unset it",
			envVar, value, envVar)}
	case !lead.UnderAgentProfiles(runtimeDir, value):
		return bindingRow{StatusWarn, fmt.Sprintf(
			"%s=%s is not a workspace profile: a lead started here runs unverified, on this shell's own credentials",
			envVar, value)}
	}
	return boundProfileRow(runtimeDir, harness, envVar, value)
}

// boundProfileRow reports a root that IS inside the agent-profiles tree. The
// spelling check runs after identity has already said yes, so a mismatch is
// the report and never the decision.
func boundProfileRow(runtimeDir, harness, envVar, value string) bindingRow {
	agent := bindingAgent(value)
	note := credentialNote(value, harness)
	// An unresolvable agent name yields no canonical path to compare against;
	// identity has already said the root is ours, so report it as bound.
	root := agentprofile.Dir(runtimeDir, agent)
	canonical := filepath.Join(root, harness)
	if root != "" && canonical != filepath.Clean(value) {
		return bindingRow{StatusWarn, fmt.Sprintf(
			"%s=%s resolves to %s by a different spelling; export %s%s",
			envVar, value, canonical, canonical, note)}
	}
	return bindingRow{StatusPass, fmt.Sprintf(
		"%s binds this shell to the %s profile (%s)%s", envVar, agent, displayDir(value), note)}
}

// bindingAgent recovers the agent a bound root belongs to
// (<...>/agent-profiles/<agent>/<harness>), so the row names who this shell
// would authenticate as.
func bindingAgent(dir string) string {
	agent := filepath.Base(filepath.Dir(filepath.Clean(dir)))
	if agent == "." || agent == string(filepath.Separator) || agent == "" {
		return "<agent>"
	}
	return agent
}

// credentialNote says whether this shell's token agrees with the bound
// profile's own, and nothing else: not the token, not a prefix of it, not its
// length. The comparison goes through supervisor.ProfileSecretEnv, which
// already owns "absent is fine, present but empty is an error" and already
// guarantees the value never reaches an error string.
func credentialNote(dir, harness string) string {
	secret, err := supervisor.ProfileSecretEnv(dir, harness)
	if err != nil {
		return "; profile credential unreadable — re-run scripts/setup-profile-token.sh"
	}
	if len(secret) == 0 {
		return "" // no credential of its own; the harness keychain still applies
	}
	key, want, ok := strings.Cut(secret[0], "=")
	if !ok {
		return ""
	}
	switch got := os.Getenv(key); {
	case got == "":
		return fmt.Sprintf("; %s unset — this shell would fall back to the harness keychain", key)
	case got == want:
		return fmt.Sprintf("; %s matches the profile", key)
	default:
		return fmt.Sprintf("; %s differs from the profile — this shell would authenticate as someone else", key)
	}
}
