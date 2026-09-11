package harnessprofile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
)

// ProfileError carries the profile directory a failure was about alongside the
// failure itself, so the caller can name a repair without re-deriving the path
// with a second filepath.Join — which is how the injector and the repair line
// would drift.
type ProfileError struct {
	Dir string
	Err error
}

func (e *ProfileError) Error() string { return e.Err.Error() }

func (e *ProfileError) Unwrap() error { return e.Err }

// FailedDir returns the profile directory err was about, or "" when err is not
// a profile failure.
func FailedDir(err error) string {
	var pe *ProfileError
	if errors.As(err, &pe) {
		return pe.Dir
	}
	return ""
}

// Enforce points an un-supervised agent at its per-agent harness profiles and
// reports the first one it is about to use that does not verify.
//
// The supervisor resolves, verifies and exports a profile root before it hands
// the environment to an agent it spawns (see AppendProfileEnv). `loom lead` and
// the standalone `loom agent` are the entry points the supervisor does not
// spawn, so without this call they get a profile only when something outside
// loom exports one — the workspace launcher script did, and every other way of
// starting them (bare `loom lead`, `loom agent <wt> --prompt <p>`, the WebUI
// terminal) silently ran the operator's own ~/.claude and ~/.codex. So they
// inject what they inherited nothing for, and verify what they did inherit.
//
// Callers refuse to start rather than falling back: unsetting the variable and
// continuing against the operator's ~/.claude is the exact leak per-agent
// profiles close. A partially injected environment is worse than no boot, so
// the first failure stops the loop.
func Enforce(runtimeDir, agent string) error {
	for _, harness := range ProfileHarnesses() {
		dir, err := Apply(runtimeDir, agent, harness)
		if err != nil {
			return &ProfileError{Dir: dir, Err: err}
		}
	}
	return nil
}

// Apply settles one harness's config root for THIS process, returning the
// profile directory a failure is about so the caller can name a repair.
//
// An inherited value wins and is only verified. That is deliberate: an operator
// who exported a config root of their own has made a choice nothing here
// provisioned, and the injection below must not override it — verifyProfile
// then leaves anything outside the workspace's agent-profiles tree alone.
func Apply(runtimeDir, agent, harness string) (string, error) {
	envVar := ProfileEnvVar(harness)
	if envVar == "" {
		return "", nil
	}
	if inherited := os.Getenv(envVar); inherited != "" {
		if err := verifyProfile(runtimeDir, inherited, harness); err != nil {
			return inherited, err
		}
		// The root is settled, but its credential still is not: the launcher
		// script exports the directory and nothing else, so without this a
		// launcher-started agent would run its own profile's config against
		// whatever token the operator's shell held.
		//
		// Only for a root this workspace provisioned, on the same boundary
		// verifyProfile draws: an operator's own config root is theirs, and
		// reading a credential out of it — let alone overriding the one they
		// exported beside it — is not this check's business.
		if !underAgentProfiles(runtimeDir, inherited) {
			return inherited, nil
		}
		secret, err := ProfileSecretEnv(inherited, harness)
		if err != nil {
			return inherited, err
		}
		return inherited, setProfileEnv(secret)
	}
	dir, assignments, err := ProfileHarnessEnv(runtimeDir, agent, harness)
	if err != nil {
		return profileDir(runtimeDir, agent, harness), err
	}
	return dir, setProfileEnv(assignments)
}

// setProfileEnv exports the assignments a profile resolved to. They arrive as
// KEY=VALUE because that is the shape the supervisor hands to exec; an
// un-supervised agent is configuring its own process instead, so it splits them
// back apart here rather than making the shared helper speak two formats.
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

// profileDir names the root a failed injection was about. Resolution goes
// through agentprofile so the repair line points at the directory the injector
// actually looked at, not a second guess at the layout.
func profileDir(runtimeDir, agent, harness string) string {
	root := agentprofile.Dir(runtimeDir, agent)
	if root == "" {
		return ""
	}
	return filepath.Join(root, harness)
}

// verifyProfile reports why the profile in configDir must not be used, or nil
// when there is nothing to check.
//
// Two configurations are deliberately NOT verified, and both must stay silent:
// an empty configDir (an unprofiled agent inheriting ~/.claude is supported),
// and a configDir outside the workspace's agent-profiles root (an operator
// pointing an agent at their own alternate config root is not this check's
// business — nothing here provisioned it and nothing here can repair it).
func verifyProfile(runtimeDir, configDir, harness string) error {
	if configDir == "" || !underAgentProfiles(runtimeDir, configDir) {
		return nil
	}
	return VerifyProfileManifest(configDir, ProfileHarnessBinary(harness))
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

// Repair names the one command that fixes this failure. The split is the whole
// point of the manifest's two guarantees: a blessed harness upgrade is
// re-recorded by `loom doctor --fix`, while anything about the profile's
// CONTENT is the operator's provisioning script — which is also the only thing
// that touches the keychain.
func Repair(err error, configDir string) string {
	if errors.Is(err, ErrProfileVersionDrift) {
		return "loom doctor --fix"
	}
	if errors.Is(err, ErrProfileTokenUnreadable) {
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
