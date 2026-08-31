package supervisor

// Per-agent harness profile roots: how a root is resolved and exported at
// spawn, what a boot does when its manifest no longer matches the harness on
// disk, and the record kept of the drifts that were allowed through.
//
// This lives beside spawn.go rather than in it: spawn.go is at its LOC ceiling,
// and the resolve/verify/boot-policy rules are read as one thing by anyone
// changing them.

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
)

// ProfileManifestName is the launch-verification manifest a provisioned
// profile root carries. Format and fingerprint scheme are documented on
// agentprofile.ManifestName, which owns the verification.
const ProfileManifestName = agentprofile.ManifestName

// Boot-refusal reasons, distinguished so an operator reading the agent's
// failure knows which repair applies: re-provision (stale fingerprint),
// re-bless the upgrade (version drift), or provision at all (no manifest).
// They are aliases of the agentprofile sentinels, so errors.Is works across
// both packages.
var (
	ErrProfileManifestMissing     = agentprofile.ErrManifestMissing
	ErrProfileManifestUnreadable  = agentprofile.ErrManifestUnreadable
	ErrProfileFingerprintMismatch = agentprofile.ErrFingerprintMismatch
	ErrProfileVersionDrift        = agentprofile.ErrVersionDrift
	ErrProfileVersionUnknown      = agentprofile.ErrVersionUnknown
)

// The two token sentinels are deliberately NOT agentprofile aliases: the
// credential file is the supervisor's concern, not the manifest's — the
// manifest does not describe it, so agentprofile has no counterpart to alias.
// Keep them here rather than "tidying" them into agentprofile.
//
// They are two sentinels rather than one because the repair differs, and both
// repair lines (`loom lead`'s and `loom doctor`'s) branch on which one it is:
// an UNREADABLE token was minted and then broken, so re-provisioning restores
// it; a MISSING one was never minted at all, and only the interactive
// setup-profile-token.sh can create it.
var (
	ErrProfileTokenUnreadable = errors.New("profile harness token unreadable")
	ErrProfileTokenMissing    = errors.New("profile harness token missing")
)

// profileHarnessEnvVar maps a profile harness root to the environment variable
// that points the harness at it. Together with agentprofile.HarnessBinary this
// is the whole export vocabulary; a new harness is one entry in each map.
var profileHarnessEnvVar = map[string]string{
	"claude": "CLAUDE_CONFIG_DIR",
	"codex":  "CODEX_HOME",
}

// profileHarnesses is the fixed order profile roots are resolved in, so an
// agent's environment is byte-identical from one boot to the next.
var profileHarnesses = []string{"claude", "codex"}

// profileTokenFile names the file inside a harness profile root that carries
// that profile's OWN long-lived credential, and profileTokenEnvVar the
// variable exporting it. Only claude has one: `claude setup-token` mints a
// per-invocation, non-rotating token and prints it instead of writing a
// credentials file, so the operator's setup-profile-token.sh captures it to
// <root>/claude/oauth-token (mode 600). codex has no equivalent, and a harness
// absent from these maps simply gets no credential injected.
//
// This is what makes a profile an IDENTITY rather than a copy of one. The
// keychain-copy fallback shares the operator's own OAuth pair across every
// profile, and the operator's next /login refresh invalidates it for whichever
// profile copied it last — the "Login expired" the agents kept hitting on an
// uncontrolled schedule. A profile carrying its own token is unaffected by
// anyone else's refresh.
//
// The token file is deliberately NOT in the manifest's file list: that list is
// an allowlist of files the fingerprint covers, and a credential must not be
// hashed into a value that is written down, compared and reported.
var (
	profileTokenFile = map[string]string{
		"claude": "oauth-token",
	}
	profileTokenEnvVar = map[string]string{
		"claude": "CLAUDE_CODE_OAUTH_TOKEN",
	}
)

// ProfileHarnesses returns the harnesses a profile root can be provisioned
// for. Callers that inject one harness at a time (`loom lead`) iterate this
// rather than writing their own list, which is how the two would drift.
func ProfileHarnesses() []string {
	return append([]string(nil), profileHarnesses...)
}

// ProfileHarnessBinary returns the binary whose --version output a harness
// profile's manifest pins, or "" for an unknown harness. Exported so a caller
// verifying a root outside the spawn path resolves the same binary the spawn
// path would, and so the provisioner's pin can be asserted against it. The
// table itself lives in agentprofile, which owns verification.
func ProfileHarnessBinary(harness string) string {
	return agentprofile.HarnessBinary[harness]
}

// ProfileEnvVar returns the environment variable a harness profile root is
// exported as, or "" for an unknown harness. It is exported so a caller can
// tell whether a variable is ALREADY set before paying for verification —
// `loom lead` must leave an inherited value alone, including an operator's own
// config root that no manifest here could ever verify.
func ProfileEnvVar(harness string) string {
	return profileHarnessEnvVar[harness]
}

// ProfileHarnessEnv resolves one harness profile root for an agent, verifies
// it, and returns the KEY=VALUE assignment that exports it — or "" when the
// agent has no such root on disk.
//
// This is the single implementation of the resolve-verify-export policy. The
// supervisor reaches it through AppendProfileEnv at spawn; `loom lead`, the one
// agent the supervisor does not spawn, calls it per harness so it can skip the
// ones whose variable it inherited. Neither may grow a second, weaker copy.
//
// An existing but unverifiable profile is a BOOT FAILURE, never a fallback to
// legacy env: silently running the agent against the operator's full ~/.claude
// is the exact leak per-agent profiles close. Per-agent boot degradation
// contains the failure to the one agent whose profile is broken.
//
// "Unverifiable" is checkProfileManifest's judgment, not agentprofile.Verify's:
// a harness version that drifted within its major boots with a recorded warning
// rather than refusing. See checkProfileManifest for why.
func ProfileHarnessEnv(projectDir, agent, harness string) (string, []string, error) {
	root := agentprofile.Dir(projectDir, agent)
	if root == "" {
		// No resolvable profile root (empty or non-segment agent name): the
		// same situation as no profile on disk, so stay on the legacy env.
		return "", nil, nil
	}
	envVar := profileHarnessEnvVar[harness]
	if envVar == "" {
		return "", nil, nil
	}
	dir := filepath.Join(root, harness)
	if !dirExists(dir) {
		return "", nil, nil
	}
	if err := checkProfileManifest(dir, agentprofile.HarnessBinary[harness]); err != nil {
		return "", nil, err
	}
	env := []string{fmt.Sprintf("%s=%s", envVar, dir)}
	secret, err := ProfileSecretEnv(dir, harness)
	if err != nil {
		return dir, nil, err
	}
	return dir, append(env, secret...), nil
}

// ProfileTokenPath returns the path to the credential file a harness profile
// root is expected to carry, or "" for a harness that has none (codex, and
// anything absent from profileTokenFile).
//
// Exported so `loom doctor` can probe for the credential without a second copy
// of the filename table: the whole point of the table is that a new harness is
// one entry, and a doctor that hardcoded "oauth-token" would silently keep
// checking claude's file for a harness that moved to another one.
func ProfileTokenPath(dir, harness string) string {
	name := profileTokenFile[harness]
	if name == "" || dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

// ProfileSecretEnv returns the assignments exporting the credential a harness
// profile root carries of its own, or nothing when the harness has no
// credential file at all (codex, and anything absent from profileTokenFile).
//
// For a harness that DOES have one, an absent token file is a boot failure —
// ErrProfileTokenMissing — exactly as a present-but-empty one already was.
// The doc here used to promise the opposite ("Absent is not an error: it is
// the pre-existing configuration"), and that sentence was the bug: it was only
// ever safe while "absent" meant "legacy profile, falls back to the shared
// keychain". The keychain fallback is gone, so an absent token now means the
// profile has NO identity — the agent boots credential-less, claims a task and
// dies on its first API call, which parks the fleet behind a stream of
// four-second exit-0 runs (277 of them from 2026-08-30).
//
// The precondition that makes an unconditional refusal safe: both call sites
// reach here only after the profile root has passed manifest verification, so
// an unmanaged directory can never arrive at this function —
//
//   - ProfileHarnessEnv calls checkProfileManifest(dir, ...) and returns early
//     on error;
//   - lead.applyLeadProfile calls it only after verifyLeadProfile and only when
//     underAgentProfiles(runtimeDir, inherited) holds, so an operator's own
//     ~/.claude is excluded and never reaches it.
//
// Do NOT add a redundant manifest-presence stat below to "make it safe on its
// own": a second gate here would drift from the real one above.
//
// It is exported for `loom lead`, the one agent the supervisor does not spawn,
// which may INHERIT its config root and so never reach ProfileHarnessEnv —
// but must still pick up that root's credential rather than run on whatever
// token the operator's shell happened to hold.
//
// Neither the token nor any prefix of it appears in the returned error, and it
// is never logged: the only place the value may go is the child's environment.
func ProfileSecretEnv(dir, harness string) ([]string, error) {
	name, envVar := profileTokenFile[harness], profileTokenEnvVar[harness]
	if name == "" || envVar == "" || dir == "" {
		return nil, nil
	}
	path := filepath.Join(dir, name)
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path derived from the workspace profile layout, not user input
	if err != nil {
		if os.IsNotExist(err) {
			// Never minted. A dangling symlink lands here too, and reporting
			// it as missing is right: the identity is not there.
			return nil, fmt.Errorf("%w: %s: profile was never minted "+
				"(run scripts/setup-profile-token.sh %s, then scripts/provision-profile.sh %s)",
				ErrProfileTokenMissing, path, profileAgentOf(dir), profileAgentOf(dir))
		}
		return nil, fmt.Errorf("%w: %s: %v", ErrProfileTokenUnreadable, path, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		// Present but empty is a broken provisioning run, not a legacy
		// profile: falling through to the operator's token would restore the
		// exact sharing this file exists to end, silently.
		return nil, fmt.Errorf("%w: %s: file is empty", ErrProfileTokenUnreadable, path)
	}
	return []string{fmt.Sprintf("%s=%s", envVar, token)}, nil
}

// profileAgentOf recovers the agent name from a profile harness root
// (<...>/agent-profiles/<agent>/<harness>), so the refusal names the profile
// an operator actually has to mint rather than making them decode a path.
func profileAgentOf(dir string) string {
	agent := filepath.Base(filepath.Dir(filepath.Clean(dir)))
	if agent == "." || agent == string(filepath.Separator) || agent == "" {
		return "<agent>"
	}
	return agent
}

// AppendProfileEnv injects every per-agent harness profile root that exists on
// disk, after verifying each one against its manifest, together with any
// credential that root carries of its own. Absent directories leave the
// environment untouched, preserving the legacy behavior of inheriting the
// operator's ~/.claude and ~/.codex.
//
// The profile's assignments are appended LAST, so a profile token overrides an
// operator token the filtered environment carried in — the allowlist passes
// CLAUDE_CODE_OAUTH_TOKEN through, and exec resolves duplicates to the final
// assignment (os/exec dedupEnv keeps the last occurrence of a key).
func AppendProfileEnv(env []string, projectDir, agent string) ([]string, error) {
	for _, harness := range profileHarnesses {
		_, assignments, err := ProfileHarnessEnv(projectDir, agent, harness)
		if err != nil {
			return nil, err
		}
		env = append(env, assignments...)
	}
	return env, nil
}

// VerifyProfileManifest applies the spawn path's verify-or-refuse rule to a
// profile root for a caller outside the daemon. `loom lead` is the one agent
// the supervisor does not spawn — the workspace launcher exports
// CLAUDE_CONFIG_DIR itself — so it must reuse this check rather than grow a
// second, weaker policy alongside it.
func VerifyProfileManifest(dir, binary string) error {
	return verifyProfileManifest(dir, binary)
}

// verifyProfileManifest verifies dir against its manifest, supplying the
// observed harness version from this package's TTL cache. binary selects which
// cached probe to use; the verification itself lives in agentprofile.
func verifyProfileManifest(dir, binary string) error {
	return agentprofile.Verify(dir, harnessVersion(binary))
}

// CheckProfileManifest applies the spawn path's BOOT policy to a profile root
// for a caller outside the daemon. `loom lead` is the one agent the supervisor
// does not spawn, so it must reuse this rather than grow a second, weaker —
// or, since this change, a second, stricter — policy alongside it.
func CheckProfileManifest(dir, binary string) error {
	return checkProfileManifest(dir, binary)
}

// checkProfileManifest applies the spawn path's boot policy to a verification
// result. It is deliberately NOT agentprofile.Verify's job: Verify reports what
// is true, and `loom doctor` wants every drift reported strictly. This decides
// what a BOOT does about it.
//
// Version drift within a major is a warning, not a refusal. The manifest pins
// the version a profile's CONTENT was provisioned against; whether the new
// harness actually works is harness-wrapper's corpus replay, which this check
// knows nothing about. Refusing here stopped the whole fleet on an ordinary
// patch bump four times in six days (2.1.235 -> .237 -> .238 -> .241 -> .243)
// and again on 2026-08-28 (.250 -> .251).
//
// A major jump still refuses, and so does an unparseable version on either
// side: those are the cases where "probably fine" is not a defensible guess.
// Every other sentinel — fingerprint mismatch above all — is untouched.
func checkProfileManifest(dir, binary string) error {
	err := verifyProfileManifest(dir, binary)
	if err == nil {
		// A profile that verifies clean is not drifted any more, whatever it
		// was when the daemon started: `loom doctor --fix` re-blesses without
		// restarting anything.
		clearProfileDrift(dir)
		return nil
	}
	if !errors.Is(err, agentprofile.ErrVersionDrift) {
		return err
	}
	m, lerr := agentprofile.LoadManifest(dir)
	if lerr != nil {
		return err // report the drift we already have, not a second fault
	}
	got := harnessVersion(binary)
	if !agentprofile.SameMajorVersion(m.HarnessVersion, got) {
		return fmt.Errorf("%w (major version change - refusing to boot)", err)
	}
	if recordProfileDrift(dir, binary, m.HarnessVersion, got) {
		// slog, not the stdlib logger the rest of spawn.go still uses: this
		// file is new, and the guard grandfathers only the old callers.
		slog.Warn("profile harness version drift, proceeding UNVERIFIED",
			"dir", dir, "manifest_version", m.HarnessVersion, "binary", binary, "observed_version", got,
			"detail", "harness-wrapper has not been verified against this version; "+
				"run `loom doctor` to see it and `loom doctor --fix` to re-bless once verified")
	}
	return nil
}

// harnessVersionTTL bounds how long a probed --version string is reused. It is
// deliberately coarse: the point is that one spawn cycle — every agent the
// supervisor brings up in a burst — costs a single probe per binary rather
// than one per agent, each of which forks a node CLI and can cost seconds.
// A harness upgrade lands within a TTL, and the next boot re-probes.
const harnessVersionTTL = 2 * time.Minute

var (
	harnessVersionMu    sync.Mutex
	harnessVersionCache = map[string]harnessVersionEntry{}
)

type harnessVersionEntry struct {
	version string
	probed  time.Time
}

// harnessVersion returns the cached "<binary> --version" first line, probing
// at most once per binary per TTL. Failures are NOT cached: a probe killed
// under load would otherwise refuse every agent boot for the whole TTL.
func harnessVersion(binary string) string {
	harnessVersionMu.Lock()
	if e, ok := harnessVersionCache[binary]; ok && time.Since(e.probed) < harnessVersionTTL {
		harnessVersionMu.Unlock()
		return e.version
	}
	harnessVersionMu.Unlock()

	version := probeHarnessVersion(binary)
	if version == "" {
		return ""
	}
	harnessVersionMu.Lock()
	harnessVersionCache[binary] = harnessVersionEntry{version: version, probed: time.Now()}
	harnessVersionMu.Unlock()
	return version
}

// ResetHarnessVersionCache drops every cached probe. For testing only: a test
// that shims a harness on PATH must not inherit a version another test — or
// the enforcement `loom lead` now runs at startup — already probed off the
// real binary.
func ResetHarnessVersionCache() {
	harnessVersionMu.Lock()
	harnessVersionCache = map[string]harnessVersionEntry{}
	harnessVersionMu.Unlock()
}

// probeHarnessVersion is a seam for tests; production runs the real binary.
var probeHarnessVersion = func(binary string) string {
	return agentprofile.ProbeVersion(binary, backends.VersionProbeTimeout)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ProfileDrift is one observed manifest-vs-binary version mismatch that was
// allowed to proceed. The supervisor records it so `loom daemon status` and
// the state file can show "running unverified" without an operator having to
// read the daemon log.
type ProfileDrift struct {
	Dir      string    `json:"dir"`
	Binary   string    `json:"binary"`
	Manifest string    `json:"manifest_version"` // version the manifest pins
	Observed string    `json:"observed_version"` // version the binary reports
	FirstAt  time.Time `json:"first_at"`
	Count    int       `json:"count"` // spawns that proceeded under this drift
}

// Package-level, guarded by a mutex, matching the harnessVersionMu /
// harnessVersionCache idiom in spawn.go: the drift is a property of the host's
// harness binaries against the workspace's profiles, not of any one Supervisor
// instance, and the same check runs from `loom lead`, which has no Supervisor
// at all.
var (
	profileDriftMu sync.Mutex
	profileDrifts  = map[string]ProfileDrift{} // keyed by profile dir
)

// recordProfileDrift records the drift and reports whether this is the first
// observation of this (dir, manifest->observed) triple, so the WARN is logged
// once per drift rather than once per spawn. Twelve agents in a restart storm
// must produce one warning line, not one per boot.
//
// A drift whose versions changed is a NEW observation: the operator needs to
// see the second upgrade too, and its count starts over so the number always
// describes the drift the line names.
func recordProfileDrift(dir, binary, manifest, observed string) (first bool) {
	profileDriftMu.Lock()
	defer profileDriftMu.Unlock()

	if d, ok := profileDrifts[dir]; ok && d.Manifest == manifest && d.Observed == observed {
		d.Count++
		profileDrifts[dir] = d
		return false
	}
	profileDrifts[dir] = ProfileDrift{
		Dir:      dir,
		Binary:   binary,
		Manifest: manifest,
		Observed: observed,
		FirstAt:  time.Now(),
		Count:    1,
	}
	return true
}

// clearProfileDrift drops dir's recorded drift. Called whenever a verification
// for dir SUCCEEDS: after `loom doctor --fix` re-blesses the pin, the recorded
// drift describes a condition that no longer exists, and a status line that
// outlives its condition is worse than no line at all.
func clearProfileDrift(dir string) {
	profileDriftMu.Lock()
	delete(profileDrifts, dir)
	profileDriftMu.Unlock()
}

// ProfileDrifts returns a snapshot of the recorded drifts, newest first.
func ProfileDrifts() []ProfileDrift {
	profileDriftMu.Lock()
	out := make([]ProfileDrift, 0, len(profileDrifts))
	for _, d := range profileDrifts {
		out = append(out, d)
	}
	profileDriftMu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		if !out[i].FirstAt.Equal(out[j].FirstAt) {
			return out[i].FirstAt.After(out[j].FirstAt)
		}
		return out[i].Dir < out[j].Dir // stable for drifts recorded in the same instant
	})
	return out
}

// ResetProfileDrifts drops the record. For testing only.
func ResetProfileDrifts() {
	profileDriftMu.Lock()
	profileDrifts = map[string]ProfileDrift{}
	profileDriftMu.Unlock()
}
