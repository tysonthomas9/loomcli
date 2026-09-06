package lead

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
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
		if err := verifyLeadProfile(runtimeDir, inherited, harness); err != nil {
			return inherited, err
		}
		// The root is settled, but its credential still is not: the launcher
		// script exports the directory and nothing else, so without this a
		// launcher-started lead would run its own profile's config against
		// whatever token the operator's shell held.
		//
		// Only for a root this workspace provisioned, on the same boundary
		// verifyLeadProfile draws: an operator's own config root is theirs,
		// and reading a credential out of it — let alone overriding the one
		// they exported beside it — is not this check's business.
		if !underAgentProfiles(runtimeDir, inherited) {
			return inherited, nil
		}
		secret, err := supervisor.ProfileSecretEnv(inherited, harness)
		if err != nil {
			return inherited, err
		}
		return inherited, setProfileEnv(secret)
	}
	dir, assignments, err := supervisor.ProfileHarnessEnv(runtimeDir, agent, harness)
	if err != nil {
		return leadProfileDir(runtimeDir, agent, harness), err
	}
	return dir, setProfileEnv(assignments)
}

// setProfileEnv exports the assignments a profile resolved to. They arrive as
// KEY=VALUE because that is the shape the supervisor hands to exec; lead is
// configuring its own process instead, so it splits them back apart here
// rather than making the shared helper speak two formats.
func setProfileEnv(assignments []string) error {
	for _, assignment := range assignments {
		key, value, ok := strings.Cut(assignment, "=")
		if !ok {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			// Never %v the assignment: one of these carries a credential.
			return fmt.Errorf("setting %s: %w", key, err)
		}
	}
	return nil
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
	// CheckProfileManifest, not VerifyProfileManifest: lead and the agents the
	// supervisor spawns must share ONE boot policy. Leaving lead strict would
	// keep the whole-fleet outage alive for the one agent that runs the fleet.
	return supervisor.CheckProfileManifest(configDir, supervisor.ProfileHarnessBinary(harness))
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
	if errors.Is(err, supervisor.ErrProfileTokenMissing) {
		// Never minted: minting alone does not materialize the token into the
		// live profile root, so both scripts are required, in this order.
		agent := profileAgentName(configDir)
		return fmt.Sprintf("scripts/setup-profile-token.sh %s && scripts/provision-profile.sh %s", agent, agent)
	}
	if errors.Is(err, supervisor.ErrProfileTokenUnreadable) {
		// A different script and a different act: provisioning copies files,
		// while minting an identity is an interactive flow a human completes.
		return fmt.Sprintf("scripts/setup-profile-token.sh %s", profileAgentName(configDir))
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

// warnUnpinnedLeadModel prints one line when the selected backend's launch
// carries no model pin, so a silently unpinned lead is not invisible.
//
// This is the only place in the launch path with an operator at a terminal.
// The resolver itself lives in backends and never prints: it also runs under
// the daemon, where this line would be noise on every spawn.
//
// Harnesses with no provisioned profile root (gemini, cursor, opencode) are
// silent — there is nothing to pin them to and nothing to repair.
func warnUnpinnedLeadModel(backend string) {
	harness := strings.ToLower(strings.TrimSpace(backend))
	if supervisor.ProfileEnvVar(harness) == "" {
		return
	}
	if backends.PinnedModelFor(harness) != "" {
		return
	}
	dir := os.Getenv(supervisor.ProfileEnvVar(harness))
	if dir == "" {
		dir = "none"
	}
	fmt.Fprintf(os.Stderr,
		"Warning: no provisioned model pin for %s (profile %s); this session starts on whatever %s holds.\n",
		harness, dir, pinnedModelFileFor(harness))
	fmt.Fprintf(os.Stderr, "Repair: scripts/provision-profile.sh %s\n\n", profileAgentName(dir))
}

// pinnedModelFileFor names, for the warning only, the live file that decides
// the model when nothing is pinned. It mirrors the managed files named in
// backends/model_pin.go; that resolver stays the source of truth.
func pinnedModelFileFor(harness string) string {
	if harness == "codex" {
		return "config.toml"
	}
	return "settings.json"
}
