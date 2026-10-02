package agentprofile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrTokenUnreadable refuses a profile whose own credential file exists but
// cannot be used (unreadable or empty).
var ErrTokenUnreadable = errors.New("profile harness token unreadable")

// harnessEnvVar maps a profile harness root to the environment variable that
// points the harness at it. Together with HarnessBinary this is the whole
// export vocabulary; a new harness is one entry in each map.
var harnessEnvVar = map[string]string{
	"claude": "CLAUDE_CONFIG_DIR",
	"codex":  "CODEX_HOME",
}

// harnesses is the fixed order profile roots are resolved in, so an agent's
// environment is byte-identical from one boot to the next.
var harnesses = []string{"claude", "codex"}

// tokenFile names the file inside a harness profile root that carries that
// profile's OWN long-lived credential, and tokenEnvVar the variable exporting
// it. Only claude has one: `claude setup-token` mints a per-invocation,
// non-rotating token and prints it instead of writing a credentials file, so
// the operator's setup-profile-token.sh captures it to <root>/claude/oauth-token
// (mode 600). codex has no equivalent, and a harness absent from these maps
// simply gets no credential injected.
//
// This is what makes a profile an IDENTITY rather than a copy of one. The
// keychain-copy fallback shares the operator's own OAuth pair across every
// profile, and the operator's next /login refresh invalidates it for whichever
// profile copied it last. A profile carrying its own token is unaffected by
// anyone else's refresh.
//
// The token file is deliberately NOT in the manifest's file list: that list is
// an allowlist of files the fingerprint covers, and a credential must not be
// hashed into a value that is written down, compared and reported.
var (
	tokenFile   = map[string]string{"claude": "oauth-token"}
	tokenEnvVar = map[string]string{"claude": "CLAUDE_CODE_OAUTH_TOKEN"}
)

// Harnesses returns the harnesses a profile root can be provisioned for, in
// resolve order. Callers that inject one harness at a time iterate this
// rather than writing their own list, which is how the two would drift.
func Harnesses() []string { return append([]string(nil), harnesses...) }

// EnvVar returns the environment variable a harness profile root is exported
// as, or "" for an unknown harness, so a caller can tell whether a variable
// is ALREADY set before paying for verification.
func EnvVar(harness string) string { return harnessEnvVar[harness] }

// HarnessEnv resolves one harness profile root for an agent, verifies it, and
// returns the root and the KEY=VALUE assignments that export it, or "" and
// nothing when the agent has no such root on disk.
//
// This is the single implementation of the resolve-verify-export policy;
// every launcher (the daemon supervisor, `loom lead`, the harness adapters)
// calls it. An existing but unverifiable profile is an error, never a
// fallback to the user's own root: silently running the agent against the
// operator's full ~/.claude is the exact leak per-agent profiles close.
func HarnessEnv(projectDir, agent, harness string) (string, []string, error) {
	root := Dir(projectDir, agent)
	if root == "" {
		// No resolvable profile root (empty or non-segment agent name): the
		// same situation as no profile on disk, so stay on the legacy env.
		return "", nil, nil
	}
	envVar := harnessEnvVar[harness]
	if envVar == "" {
		return "", nil, nil
	}
	dir := filepath.Join(root, harness)
	if !dirExists(dir) {
		return "", nil, nil
	}
	if err := VerifyRoot(dir, HarnessBinary[harness]); err != nil {
		return "", nil, err
	}
	env := []string{fmt.Sprintf("%s=%s", envVar, dir)}
	secret, err := SecretEnv(dir, harness)
	if err != nil {
		return dir, nil, err
	}
	return dir, append(env, secret...), nil
}

// SecretEnv returns the assignments exporting the credential a harness
// profile root carries of its own, or nothing when it carries none. Absent is
// not an error: it is the pre-existing configuration. A launcher that
// INHERITS its config root (`loom lead`) calls it directly, so it still picks
// up that root's credential.
//
// Neither the token nor any prefix of it appears in the returned error, and it
// is never logged: the only place the value may go is the child's environment.
func SecretEnv(dir, harness string) ([]string, error) {
	name, envVar := tokenFile[harness], tokenEnvVar[harness]
	if name == "" || envVar == "" || dir == "" {
		return nil, nil
	}
	path := filepath.Join(dir, name)
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path derived from the workspace profile layout, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %s: %v", ErrTokenUnreadable, path, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		// Present but empty is a broken provisioning run, not a legacy
		// profile: falling through to the operator's token would restore the
		// exact sharing this file exists to end, silently.
		return nil, fmt.Errorf("%w: %s: file is empty", ErrTokenUnreadable, path)
	}
	return []string{fmt.Sprintf("%s=%s", envVar, token)}, nil
}

// VerifyRoot verifies dir against its manifest, supplying the observed
// harness version of binary from a TTL cache.
func VerifyRoot(dir, binary string) error { return Verify(dir, cachedVersion(binary)) }

// versionTTL bounds how long a probed --version string is reused. One spawn
// cycle, every agent brought up in a burst, then costs a single probe per
// binary rather than one per agent, each of which forks a node CLI. A harness
// upgrade lands within a TTL, and the next boot re-probes.
const versionTTL = 2 * time.Minute

// ProbeTimeout bounds one --version probe (the same 20 s as the backends
// layer's VersionProbeTimeout).
var ProbeTimeout = 20 * time.Second

// ProbeVersionFunc is the version probe; tests replace it.
var ProbeVersionFunc = func(binary string) string { return ProbeVersion(binary, ProbeTimeout) }

var (
	versionMu    sync.Mutex
	versionCache = map[string]versionEntry{}
)

type versionEntry struct {
	version string
	probed  time.Time
}

// cachedVersion returns the cached "<binary> --version" first line, probing
// at most once per binary per TTL. Failures are NOT cached: a probe killed
// under load would otherwise refuse every agent boot for the whole TTL.
func cachedVersion(binary string) string {
	versionMu.Lock()
	if e, ok := versionCache[binary]; ok && time.Since(e.probed) < versionTTL {
		versionMu.Unlock()
		return e.version
	}
	versionMu.Unlock()

	version := ProbeVersionFunc(binary)
	if version == "" {
		return ""
	}
	versionMu.Lock()
	versionCache[binary] = versionEntry{version: version, probed: time.Now()}
	versionMu.Unlock()
	return version
}

// ResetVersionCache drops every cached probe. For tests: a test that shims a
// harness on PATH must not inherit a version another test already probed.
func ResetVersionCache() {
	versionMu.Lock()
	versionCache = map[string]versionEntry{}
	versionMu.Unlock()
}
