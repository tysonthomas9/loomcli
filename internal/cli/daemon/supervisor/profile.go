package supervisor

// The per-agent harness profile boot path: resolving and exporting an agent's
// profile roots, the manifest/credential boot policy, and the harness version
// probe cache. Moved out of spawn.go unchanged (LOC gate); spawn.go keeps the
// one call site, appendRuntimeEnv.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
)

// AgentProfilesDirName is the workspace-relative root holding per-agent
// harness profile directories: .loom/agent-profiles/<worktree>/{claude,codex}.
// When a backend subdirectory exists for an agent, the supervisor exports the
// matching harness config-root variable (CLAUDE_CONFIG_DIR / CODEX_HOME) into
// the agent process. envfilter allowlists both names, so the value flows
// unchanged through cli.FilteredEnv() into the harness child built by the
// backends layer — no change is needed there.
//
// Directory existence is the whole contract: there is no config key and no
// flag, so the same layout works unchanged inside a container image.
//
// It is an alias, not a second literal: agentprofile owns the layout, and the
// readers (transcript mirroring, `loom doctor`) resolve it from there.
const AgentProfilesDirName = agentprofile.DirName

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
	ErrProfileManagedContentDrift = agentprofile.ErrManagedContentDrift
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

// ErrProfileCodexAuthMissing is the codex counterpart, and deliberately ONE
// sentinel where claude has two: absent, unreadable, unparseable and
// tokens-less auth.json all have the same repair — a dedicated `codex login`
// into that root. Splitting it would produce four repair lines that all say
// the same command.
var ErrProfileCodexAuthMissing = errors.New("profile codex auth missing")

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
//
// codex is ABSENT from both maps and must stay absent. It authenticates with
// ChatGPT OAuth (`auth_mode: chatgpt`), whose refresh_token ROTATES: codex
// rewrites auth.json for itself as it refreshes. There is no static
// per-profile codex secret to inject, and wiring a rotating credential into an
// env var pinned at spawn is how a profile ends up presenting a token codex
// has already spent — the `refresh_token_reused` 401. API-key mode, which
// would give codex a static secret, is a rejected option and not a fallback.
// codex's identity is therefore the FILE, not an injection: see
// profileAuthFile and CheckProfileAuth below.
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
	if err := verifyProfileManifest(dir, agentprofile.HarnessBinary[harness]); err != nil {
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

// profileAuthFile names the file inside a harness profile root that IS that
// profile's identity — the login codex writes and refreshes for itself. The
// distinction from profileTokenFile above is the whole point of two tables:
// profileTokenFile names a credential loom INJECTS into the child's
// environment, while profileAuthFile names a file loom only ever reads to
// answer "does this profile have a login of its own?". Nothing here is
// exported into any environment.
//
// Only codex has one. A harness absent from this table short-circuits every
// check below, which is what keeps claude's path byte-identical.
var profileAuthFile = map[string]string{
	"codex": "auth.json",
}

// ProfileAuthPath returns the path to the login file a harness profile root
// owns, or "" for a harness that has none (claude, and anything absent from
// profileAuthFile). Exported for the same reason as ProfileTokenPath: `loom
// doctor` must not carry a second copy of the filename.
func ProfileAuthPath(dir, harness string) string {
	name := profileAuthFile[harness]
	if name == "" || dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

// codexAuth is the slice of codex's auth.json this package reads. It is
// deliberately partial: everything else in that file is codex's business, and
// a struct that named more fields would start to look like a schema loom owns.
type codexAuth struct {
	Tokens *struct {
		AccountID    string `json:"account_id"`
		RefreshToken string `json:"refresh_token"`
	} `json:"tokens"`
}

// CheckProfileAuth reports why a harness profile root has no login of its own,
// or nil when it has one — or when the harness carries no login file at all
// (claude, and anything absent from profileAuthFile, which returns here).
//
// This is the codex half of "a profile is an IDENTITY, not a copy of one". A
// codex root with no auth.json is not a legacy profile falling back to
// ~/.codex: the supervisor exports CODEX_HOME at it, so codex sees an empty
// home, and the agent boots logged-OUT — it claims a task and dies on its
// first call, exactly the four-second exit-0 loop the claude token check was
// added to stop.
//
// Deliberately NOT checked: token expiry, `last_refresh`, or anything else
// time-based. codex refreshes its own tokens whenever it runs, so an access
// token that expired an hour ago and a `last_refresh` from three months back
// both describe a perfectly working profile. Refusing on a timestamp would
// ground a profile that is fine, which is a worse failure than the one this
// check exists to catch.
//
// Every check is a file read. No network call is made on any path.
func CheckProfileAuth(dir, harness string) error {
	path := ProfileAuthPath(dir, harness)
	if path == "" {
		return nil // this harness owns no login file
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path derived from the workspace profile layout, not user input
	if err != nil {
		if os.IsNotExist(err) {
			// A dangling symlink lands here too, and "missing" is the right
			// reading: the identity is not there.
			return fmt.Errorf("%w: %s: profile has no codex login (run CODEX_HOME=%s codex login)",
				ErrProfileCodexAuthMissing, path, dir)
		}
		return fmt.Errorf("%w: %s: unreadable: %v", ErrProfileCodexAuthMissing, path, err)
	}
	var auth codexAuth
	if err := json.Unmarshal(raw, &auth); err != nil {
		// %v of a json error can quote the offending input, so this must never
		// be handed the raw bytes: encoding/json reports offsets, not content.
		return fmt.Errorf("%w: %s: unparseable: %v", ErrProfileCodexAuthMissing, path, err)
	}
	if auth.Tokens == nil || auth.Tokens.RefreshToken == "" {
		// `auth_mode: apikey` with a null `tokens` lands here, and refusing is
		// right: API-key mode is not how this fleet runs codex, and a profile
		// in it has no ChatGPT identity of its own.
		return fmt.Errorf("%w: %s: no tokens object (run CODEX_HOME=%s codex login)",
			ErrProfileCodexAuthMissing, path, dir)
	}
	return nil
}

// ProfileAuthIdentity returns the account the login at path belongs to and a
// short fingerprint of its refresh token, for `loom doctor`'s cross-profile
// pass. The raw refresh token never leaves this function: the fingerprint is
// the first 8 hex characters of its SHA-256, which is enough to tell two roots
// sharing one credential from two independent logins and useless for anything
// else.
func ProfileAuthIdentity(path string) (accountID, refreshFingerprint string, err error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path derived from the workspace profile layout, not user input
	if err != nil {
		return "", "", fmt.Errorf("%w: %s: %v", ErrProfileCodexAuthMissing, path, err)
	}
	var auth codexAuth
	if err := json.Unmarshal(raw, &auth); err != nil {
		return "", "", fmt.Errorf("%w: %s: unparseable: %v", ErrProfileCodexAuthMissing, path, err)
	}
	if auth.Tokens == nil || auth.Tokens.RefreshToken == "" {
		return "", "", fmt.Errorf("%w: %s: no tokens object", ErrProfileCodexAuthMissing, path)
	}
	sum := sha256.Sum256([]byte(auth.Tokens.RefreshToken))
	return auth.Tokens.AccountID, hex.EncodeToString(sum[:])[:8], nil
}

// ProfileSecretEnv is the single credential gate every boot path funnels
// through, and it now covers BOTH shapes a profile identity comes in: the
// injected token claude carries (below) and the owned login codex writes for
// itself (CheckProfileAuth, first). A harness with neither returns nothing.
//
// Both shapes are checked here rather than in the two callers because there
// are exactly two of them — ProfileHarnessEnv and lead.applyLeadProfile's
// inherited arm — and a check added to one of them is a check the other
// silently does not have.
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
//   - ProfileHarnessEnv calls verifyProfileManifest(dir, ...) and returns early
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
	// codex's identity is a file it owns, not a value loom injects, so this
	// gate runs first and returns nothing to export when it passes.
	if err := CheckProfileAuth(dir, harness); err != nil {
		return nil, err
	}
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
// assignment.
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
