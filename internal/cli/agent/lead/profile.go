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

// leadProfileHarness is the harness whose profile root `loom lead` can be
// pointed at. Only Claude has an interactive launcher today; CODEX_HOME is not
// on the envfilter allowlist, so it never reaches an interactive lead.
const leadProfileHarness = "claude"

// enforceLeadProfile refuses to start when CLAUDE_CONFIG_DIR points at a
// workspace agent profile that no longer verifies, and is silent otherwise.
//
// The supervisor verifies a profile before it exports the variable to an agent
// it spawns (see appendProfileEnv). `loom lead` is the one agent that does not
// come from the supervisor: the workspace launcher exports CLAUDE_CONFIG_DIR
// itself and lead inherits it through the envfilter allowlist, so without this
// call a drifted or tampered profile takes effect silently — on the agent that
// runs in the operator's own terminal.
//
// It refuses rather than falling back: unsetting the variable and continuing
// against the operator's ~/.claude is the exact leak per-agent profiles close.
func enforceLeadProfile() {
	err := verifyLeadProfile(cli.GetWorkspaceRuntimeDir(), os.Getenv("CLAUDE_CONFIG_DIR"))
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	fmt.Fprintf(os.Stderr, "Repair: %s\n", leadProfileRepair(err, os.Getenv("CLAUDE_CONFIG_DIR")))
	os.Exit(1)
}

// verifyLeadProfile reports why the profile in configDir must not be used, or
// nil when there is nothing to check.
//
// Two configurations are deliberately NOT verified, and both must stay silent:
// an empty configDir (an unprofiled lead inheriting ~/.claude is supported),
// and a configDir outside the workspace's agent-profiles root (an operator
// pointing lead at their own alternate config root is not this check's
// business — nothing here provisioned it and nothing here can repair it).
func verifyLeadProfile(runtimeDir, configDir string) error {
	if configDir == "" || !underAgentProfiles(runtimeDir, configDir) {
		return nil
	}
	return supervisor.VerifyProfileManifest(configDir, leadProfileHarness)
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
