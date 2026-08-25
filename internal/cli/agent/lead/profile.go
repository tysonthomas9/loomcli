package lead

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// enforceLeadProfile points `loom lead` at its per-agent harness profiles and
// refuses to start when one it is about to use does not verify.
//
// The supervisor resolves, verifies and exports a profile root before it hands
// the environment to an agent it spawns (see supervisor.AppendProfileEnv).
// `loom lead` is the one agent that does not come from the supervisor, so
// without this call it gets a profile only when something outside loom exports
// one — the workspace launcher script did, and every other way of starting a
// lead (bare `loom lead`, the WebUI terminal) silently ran the operator's own
// ~/.claude and ~/.codex. So lead injects what it inherited nothing for, and
// verifies what it did inherit.
//
// It refuses rather than falling back: unsetting the variable and continuing
// against the operator's ~/.claude is the exact leak per-agent profiles close.
func enforceLeadProfile() {
	runtimeDir := cli.GetWorkspaceRuntimeDir()
	agent := resolveLeadAgentID()
	for _, harness := range supervisor.ProfileHarnesses() {
		if dir, err := applyLeadProfile(runtimeDir, agent, harness); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			fmt.Fprintf(os.Stderr, "Repair: %s\n", leadProfileRepair(err, dir))
			os.Exit(1)
		}
	}
}

// applyLeadProfile settles one harness's config root for this lead, returning
// the profile directory the failure is about so the caller can name a repair.
//
// An inherited value wins and is only verified. That is deliberate: an operator
// who exported a config root of their own has made a choice nothing here
// provisioned, and the injection below must not override it — verifyLeadProfile
// then leaves anything outside the workspace's agent-profiles tree alone.
func applyLeadProfile(runtimeDir, agent, harness string) (string, error) {
	envVar := supervisor.ProfileEnvVar(harness)
	if envVar == "" {
		return "", nil
	}
	if inherited := os.Getenv(envVar); inherited != "" {
		return inherited, verifyLeadProfile(runtimeDir, inherited, harness)
	}
	assignment, err := supervisor.ProfileHarnessEnv(runtimeDir, agent, harness)
	if err != nil {
		return leadProfileDir(runtimeDir, agent, harness), err
	}
	if assignment == "" {
		return "", nil
	}
	_, dir, _ := strings.Cut(assignment, "=")
	return dir, os.Setenv(envVar, dir)
}

// leadProfileDir names the root a failed injection was about. Resolution goes
// through agentprofile so the repair line points at the directory the injector
// actually looked at, not a second guess at the layout.
func leadProfileDir(runtimeDir, agent, harness string) string {
	root := agentprofile.Dir(runtimeDir, agent)
	if root == "" {
		return ""
	}
	return filepath.Join(root, harness)
}

// verifyLeadProfile reports why the profile in configDir must not be used, or
// nil when there is nothing to check.
//
// Two configurations are deliberately NOT verified, and both must stay silent:
// an empty configDir (an unprofiled lead inheriting ~/.claude is supported),
// and a configDir outside the workspace's agent-profiles root (an operator
// pointing lead at their own alternate config root is not this check's
// business — nothing here provisioned it and nothing here can repair it).
func verifyLeadProfile(runtimeDir, configDir, harness string) error {
	if configDir == "" || !underAgentProfiles(runtimeDir, configDir) {
		return nil
	}
	return supervisor.VerifyProfileManifest(configDir, supervisor.ProfileHarnessBinary(harness))
}

// underAgentProfiles reports whether configDir sits inside
// <runtimeDir>/.loom/agent-profiles/. The root itself does not count: a
// profile is always at least <agent>/<harness> below it.
func underAgentProfiles(runtimeDir, configDir string) bool {
	if runtimeDir == "" {
		return false
	}
	root, err := filepath.Abs(filepath.Join(runtimeDir, ".loom", agentprofile.DirName))
	if err != nil {
		return false
	}
	dir, err := filepath.Abs(configDir)
	if err != nil {
		return false
	}
	return strings.HasPrefix(dir, root+string(filepath.Separator))
}

// leadProfileRepair names the one command that fixes this failure. The split
// is the whole point of the manifest's two guarantees: a blessed harness
// upgrade is re-recorded by `loom doctor --fix`, while anything about the
// profile's CONTENT is the operator's provisioning script — which is also the
// only thing that touches the keychain.
func leadProfileRepair(err error, configDir string) string {
	if errors.Is(err, supervisor.ErrProfileVersionDrift) {
		return "loom doctor --fix"
	}
	return fmt.Sprintf("scripts/provision-profile.sh %s", profileAgentName(configDir))
}

// profileAgentName recovers the agent a profile root belongs to
// (<...>/agent-profiles/<agent>/<harness>), so the repair line names the
// profile the operator actually has to re-provision.
func profileAgentName(configDir string) string {
	agent := filepath.Base(filepath.Dir(filepath.Clean(configDir)))
	if agent == "." || agent == string(filepath.Separator) || agent == "" {
		return "<agent>"
	}
	return agent
}
