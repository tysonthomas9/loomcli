// Package harnessprofile is the single implementation of the per-agent harness
// profile policy: resolve an agent's profile root, verify it against its
// manifest, and export that root — and any credential it carries of its own —
// to the harness process.
//
// It lives below the CLI command packages rather than inside the supervisor
// because there are three callers and one of them cannot import the others.
// `internal/cli/daemon/supervisor` already imports `internal/cli/agent`, so
// the standalone `loom agent` path can never import the supervisor, and
// interposing a helper only makes the cycle one hop longer. The policy
// therefore moved DOWN, to a leaf all three import: the supervisor at spawn
// (AppendProfileEnv), `loom lead` at startup, and `loom agent` at startup
// (Enforce). The one-implementation rule the doc comments below state — no
// second, weaker copy — only holds in this shape.
package harnessprofile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
)

// The profile layout's names, re-exported so a caller that already imports this
// package for the policy does not need a second import for the two constants
// that go with it. They are aliases: agentprofile owns the layout and the
// verification, and the readers (transcript mirroring, `loom doctor`) resolve
// them from there.
const (
	// DirName is the directory, under a workspace's .loom, that holds one
	// subtree per agent.
	DirName = agentprofile.DirName
	// ManifestName is the launch-verification manifest a provisioned profile
	// root carries. Format and fingerprint scheme are documented on
	// agentprofile.ManifestName.
	ManifestName = agentprofile.ManifestName
)

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

// ErrProfileTokenUnreadable is deliberately NOT an agentprofile alias: the
// credential file is the supervisor's concern, not the manifest's — the
// manifest does not describe it, so agentprofile has no counterpart to alias.
// Keep it here rather than "tidying" it into agentprofile.
var ErrProfileTokenUnreadable = errors.New("profile harness token unreadable")

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

// ProfileSecretEnv returns the assignments exporting the credential a harness
// profile root carries of its own, or nothing when it carries none — which is
// every profile that has not been migrated to a setup-token identity yet, and
// every harness that has no such file at all. Absent is not an error: it is
// the pre-existing configuration, and it must keep working unchanged.
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
			return nil, nil
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
