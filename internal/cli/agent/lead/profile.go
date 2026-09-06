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

// errRelativeProfileRoot rejects an inherited config root that is not
// absolute. The harness resolves it against ITS cwd, so the same value names
// a different directory from every worktree — which is how a lead ends up
// bound to a root nothing provisioned while every check reads as green.
var errRelativeProfileRoot = errors.New("harness config root must be an absolute path")

// applyLeadProfile settles one harness's config root for this lead, returning
// the profile directory the failure is about so the caller can name a repair.
//
// An inherited value wins and is only verified. That is deliberate: an operator
// who exported a config root of their own has made a choice nothing here
// provisioned, and the injection below must not override it — verifyLeadProfile
// then leaves anything outside the workspace's agent-profiles tree alone. The
// boundary between "ours" and "theirs" is filesystem identity, not path
// spelling; see underAgentProfiles. An inherited value that is not absolute is
// refused outright rather than classified.
func applyLeadProfile(runtimeDir, agent, harness string) (string, error) {
	envVar := supervisor.ProfileEnvVar(harness)
	if envVar == "" {
		return "", nil
	}
	if inherited := os.Getenv(envVar); inherited != "" {
		// A relative config root is never a deliberate choice: the harness
		// resolves it against ITS cwd, so the same value names a different
		// directory from every worktree — which is how a lead ends up bound
		// to a root nothing provisioned while every check reads as green.
		// Refused before any harness probe, so an unprofiled machine does not
		// pay a --version fork to be told about a bad env value.
		if !filepath.IsAbs(inherited) {
			return inherited, fmt.Errorf("%w: %s=%s", errRelativeProfileRoot, envVar, inherited)
		}
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
//
// "Outside" is decided by filesystem identity, not by comparing path strings:
// a second spelling of a provisioned root is still that root.
func verifyLeadProfile(runtimeDir, configDir, harness string) error {
	if configDir == "" || !underAgentProfiles(runtimeDir, configDir) {
		return nil
	}
	return supervisor.VerifyProfileManifest(configDir, supervisor.ProfileHarnessBinary(harness))
}

// UnderAgentProfiles reports whether configDir sits inside
// <runtimeDir>/.loom/agent-profiles/. It is exported so `loom doctor` can
// report the binding a lead WOULD get using the same predicate the lead
// itself decides by — two answers to this one question is the whole class of
// bug this function has already produced once.
func UnderAgentProfiles(runtimeDir, configDir string) bool {
	return underAgentProfiles(runtimeDir, configDir)
}

// underAgentProfiles reports whether configDir sits inside
// <runtimeDir>/.loom/agent-profiles/. The root itself does not count: a
// profile is always at least <agent>/<harness> below it.
//
// The comparison is by FILESYSTEM IDENTITY, not by path spelling. Two
// spellings of one directory are routine here — a case-insensitive macOS
// volume renders the workspace as both `puppet` and `PUPPET`, /tmp is a
// symlink to /private/tmp, and a relative env value resolves against the
// caller's cwd rather than the runtime dir. A prefix comparison read all
// three as "an operator's own config root", which silently disabled both
// manifest verification AND credential injection for a root this workspace
// had provisioned (PUPPET-523).
func underAgentProfiles(runtimeDir, configDir string) bool {
	if runtimeDir == "" || configDir == "" {
		return false
	}
	rootInfo, err := os.Stat(filepath.Join(runtimeDir, ".loom", agentprofile.DirName))
	if err != nil {
		// No agent-profiles tree at all: nothing here provisioned anything,
		// so no config root can be ours.
		return false
	}
	dir, err := filepath.Abs(configDir)
	if err != nil {
		return false
	}
	// Walk up from configDir's PARENT: reaching the root means configDir is
	// at least one level below it, which is the "root itself does not count"
	// rule the callers and their tests depend on.
	for {
		parent := filepath.Dir(dir)
		if parent == dir { // converged on "/" (or a volume root)
			return false
		}
		// Stat follows symlinks and the kernel folds case on a
		// case-insensitive volume, so this is true for every spelling of the
		// same directory and false for a genuinely different one. An
		// unreadable intermediate directory is skipped, never fatal: it must
		// not flip a real profile to "an operator's own".
		if info, err := os.Stat(parent); err == nil && os.SameFile(info, rootInfo) {
			return true
		}
		dir = parent
	}
}

// leadProfileRepair names the one command that fixes this failure. The split
// is the whole point of the manifest's two guarantees: a blessed harness
// upgrade is re-recorded by `loom doctor --fix`, while anything about the
// profile's CONTENT is the operator's provisioning script — which is also the
// only thing that touches the keychain.
func leadProfileRepair(err error, configDir string) string {
	// First, because profileAgentName on a relative path yields junk. The
	// variable's name is already in the error text, so this stays generic
	// across harnesses.
	if errors.Is(err, errRelativeProfileRoot) {
		return "export an absolute path (CLAUDE_CONFIG_DIR=$(cd <dir> && pwd)), or unset it and let `loom lead` resolve its own profile"
	}
	if errors.Is(err, supervisor.ErrProfileVersionDrift) {
		return "loom doctor --fix"
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
