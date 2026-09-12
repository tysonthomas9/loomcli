package agentprofile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ManifestName is the launch-verification manifest a provisioned profile root
// carries. Format is fixed by the Agent Home Profiles design and written by the
// operator's provision-profile script:
//
//	{"files": [...sorted relative paths...],
//	 "managed": [...sorted relative paths...],
//	 "fingerprint": "<sha256 hex>",
//	 "harness_version": "<claude|codex --version, first line trimmed>"}
//
// fingerprint = sha256 over the concatenation, in listed order, of
// (relative path + NUL + file bytes). The list is an ALLOWLIST: files not
// named in it are ignored entirely, because the harness owns them and mutates
// them at runtime by design (.credentials.json, .claude.json, sessions/).
//
// The two lists are two different allowlists over two different schemes:
//
//   - `files` is the BYTE allowlist. Every entry is hashed into `fingerprint`,
//     so every entry must be content nothing but the provisioner ever writes.
//   - `managed` is the SEMANTIC allowlist: provisioned content the harness
//     legitimately rewrites (settings.json). Nothing in it is byte-hashed. Its
//     pristine baseline lives at .provisioned/<rel>, which IS in `files`, and
//     the live file is verified against that baseline as a subset — see
//     managed.go. `managed` is optional; a manifest without it verifies
//     exactly as it always did.
//
// The manifest lives here rather than in the supervisor because three callers
// now read it — spawn, `loom doctor` (no daemon) and `loom lead` (not
// supervisor-spawned) — and a second implementation of the fingerprint scheme
// is how the verifier and the repairer silently stop agreeing.
const ManifestName = ".manifest.json"

// HarnessBinary maps a profile harness root to the binary whose --version
// output the manifest pins. Resolution is by bare name on PATH, exactly as the
// backends layer launches them.
var HarnessBinary = map[string]string{
	"claude": "claude",
	"codex":  "codex",
}

// harnessOrder is the fixed iteration order for harness roots, so enumeration
// and injection visit them identically regardless of map ordering.
var harnessOrder = []string{"claude", "codex"}

// Manifest is the parsed .manifest.json. Field order is the provisioner's dict
// order, so a re-marshaled manifest diffs clean against a fresh provision.
type Manifest struct {
	Files []string `json:"files"`
	// Managed is positioned after Files because that is the provisioner's dict
	// order; omitempty keeps a pre-`managed` manifest re-marshaling unchanged.
	Managed        []string `json:"managed,omitempty"`
	Fingerprint    string   `json:"fingerprint"`
	HarnessVersion string   `json:"harness_version"`
}

// Verification failure reasons, distinguished so an operator reading the
// failure knows which repair applies: re-provision (stale fingerprint),
// re-bless the upgrade (version drift), or provision at all (no manifest).
var (
	ErrManifestMissing     = errors.New("profile manifest missing")
	ErrManifestUnreadable  = errors.New("profile manifest unreadable")
	ErrFingerprintMismatch = errors.New("profile fingerprint mismatch")
	ErrManagedContentDrift = errors.New("profile managed content drift")
	ErrVersionDrift        = errors.New("profile harness version drift")
	ErrVersionUnknown      = errors.New("profile harness version unknown")
)

// LoadManifest reads and parses dir's manifest. A dir that exists but was
// never provisioned yields ErrManifestMissing; anything else unreadable or
// unparseable yields ErrManifestUnreadable.
func LoadManifest(dir string) (Manifest, error) {
	var m Manifest
	raw, err := os.ReadFile(filepath.Join(dir, ManifestName)) //nolint:gosec // G304: path derived from the workspace profile layout, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return m, fmt.Errorf("%w: %s (profile dir exists but was never provisioned)", ErrManifestMissing, dir)
		}
		return m, fmt.Errorf("%w: %s: %v", ErrManifestUnreadable, dir, err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("%w: %s: %v", ErrManifestUnreadable, dir, err)
	}
	return m, nil
}

// Fingerprint hashes the listed files in listed order. Files present in dir but
// absent from the list are not read at all — the manifest is an allowlist, and
// the harness-owned remainder legitimately changes underfoot.
func Fingerprint(dir string, files []string) (string, error) {
	h := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, rel)) //nolint:gosec // G304: rel comes from the profile's own manifest
		if err != nil {
			return "", fmt.Errorf("manifested file %q: %w", rel, err)
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Verify recomputes dir's fingerprint from the files the manifest lists and
// compares the pinned harness version against gotVersion. Every error names the
// profile directory, so the failure an operator reads points at the thing to
// repair.
//
// The observed version is a PARAMETER rather than probed here: the probe is an
// expensive fork the supervisor caches behind a TTL, and keeping it at the call
// site makes the whole comparison testable with no binary on PATH. The binary
// name the two version messages interpolate is derived from the directory —
// every real profile root is .../claude or .../codex — so callers need not
// thread it through.
func Verify(dir, gotVersion string) error {
	m, err := LoadManifest(dir)
	if err != nil {
		return err
	}

	sum, err := Fingerprint(dir, m.Files)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrManifestUnreadable, dir, err)
	}
	if sum != m.Fingerprint {
		return fmt.Errorf("%w: %s: manifest %s, on disk %s (re-provision the profile)",
			ErrFingerprintMismatch, dir, shortSum(m.Fingerprint), shortSum(sum))
	}

	// Content faults outrank version faults, so this precedes the version
	// comparison: a soft-failed version drift must never mask a real change to
	// what the agent is actually configured to do.
	if err := verifyManaged(dir, m.Managed); err != nil {
		return err
	}

	binary := binaryFor(dir)
	if gotVersion == "" {
		return fmt.Errorf("%w: %s: %s --version produced nothing", ErrVersionUnknown, dir, binary)
	}
	if gotVersion != m.HarnessVersion {
		return fmt.Errorf("%w: %s: manifest pins %q, %s reports %q (re-provision to bless the upgrade)",
			ErrVersionDrift, dir, m.HarnessVersion, binary, gotVersion)
	}
	return nil
}

// Bless rewrites ONLY the harness_version field of dir's manifest, after
// re-verifying the fingerprint. It is the supported answer to "the harness
// auto-updated and the profile content is unchanged".
//
// It refuses on a fingerprint mismatch, and equally on managed content drift:
// those are different faults with a different repair (re-run the operator's
// provisioner), and blessing either would launder unprovisioned content past
// the check the manifest exists to make. Both halves of the verification are
// re-run here for exactly that reason — "never bless content it has not
// verified" is a property of Bless, not of the caller.
// It never creates a manifest that did not exist — provisioning is not this
// function's job — and it is reachable only from an operator-typed command;
// nothing in the daemon re-blesses on its own.
func Bless(dir, gotVersion string) error {
	path := filepath.Join(dir, ManifestName)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s (profile dir exists but was never provisioned)", ErrManifestMissing, dir)
		}
		return fmt.Errorf("%w: %s: %v", ErrManifestUnreadable, dir, err)
	}

	m, err := LoadManifest(dir)
	if err != nil {
		return err
	}

	sum, err := Fingerprint(dir, m.Files)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrManifestUnreadable, dir, err)
	}
	if sum != m.Fingerprint {
		return fmt.Errorf("%w: %s: manifest %s, on disk %s (re-provision the profile)",
			ErrFingerprintMismatch, dir, shortSum(m.Fingerprint), shortSum(sum))
	}
	if err := verifyManaged(dir, m.Managed); err != nil {
		return err
	}

	if gotVersion == "" {
		return fmt.Errorf("%w: %s: %s --version produced nothing", ErrVersionUnknown, dir, binaryFor(dir))
	}
	if m.HarnessVersion == gotVersion {
		return nil // idempotent: already blessed, write nothing
	}

	m.HarnessVersion = gotVersion
	raw, err := marshalManifest(m)
	if err != nil {
		return err
	}
	return writeManifestAtomically(path, dir, raw, info.Mode().Perm())
}

// marshalManifest reproduces the provisioner's json.dump(manifest, f, indent=1)
// byte-for-byte for the ASCII case: one-space indent, no HTML escaping, and no
// trailing newline. A later full re-provision must produce no spurious diff.
// (Python's default ensure_ascii escapes non-ASCII where Go emits raw UTF-8;
// byte parity there is not achievable and does not matter — .manifest.json is
// never part of its own fingerprint.)
func marshalManifest(m Manifest) ([]byte, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", " ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSuffix(buf.String(), "\n")), nil
}

// writeManifestAtomically writes via a temp file in dir and renames over path,
// so the manifest is never observed half-written. The temp file is safe inside
// the profile root: the manifest is an allowlist and never lists itself, so
// neither the rewrite nor a transient sibling can invalidate the fingerprint.
func writeManifestAtomically(path, dir string, raw []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(dir, ManifestName+".tmp-*")
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrManifestUnreadable, dir, err)
	}
	tmp := f.Name()
	cleanup := func(cause error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: %s: %v", ErrManifestUnreadable, dir, cause)
	}
	if _, err := f.Write(raw); err != nil {
		return cleanup(err)
	}
	// os.CreateTemp makes 0600; the provisioner's manifests are 0644.
	if err := f.Chmod(mode); err != nil {
		return cleanup(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: %s: %v", ErrManifestUnreadable, dir, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: %s: %v", ErrManifestUnreadable, dir, err)
	}
	return nil
}

// Profile is one provisioned harness root: agent "observer", harness "claude",
// dir <projectDir>/.loom/agent-profiles/observer/claude.
type Profile struct {
	Agent   string
	Harness string
	Dir     string
}

// List enumerates every provisioned harness root under projectDir, sorted by
// (agent, harness). It returns (nil, nil) when the agent-profiles root does not
// exist — a fleet with no profiles is not an error, it is the default.
//
// It walks the DIRECTORY, not the daemon's agent roster, which is what lets a
// caller see an agent the daemon has never heard of (`lead`).
func List(projectDir string) ([]Profile, error) {
	root := filepath.Join(projectDir, ".loom", DirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Profile
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		agentDir := Dir(projectDir, e.Name())
		if agentDir == "" {
			continue
		}
		for _, harness := range harnessOrder {
			dir := filepath.Join(agentDir, harness)
			if !dirExists(dir) {
				continue
			}
			out = append(out, Profile{Agent: e.Name(), Harness: harness, Dir: dir})
		}
	}
	return out, nil
}

// ProbeVersion returns the first line of "<binary> --version", trimmed, or ""
// on any failure. It does not cache: callers that probe per spawn cycle own
// that policy, and a probe killed under load must not refuse every boot.
func ProbeVersion(binary string, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version") //nolint:gosec // G204: binary is one of the fixed harness names above
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

// binaryFor derives the harness binary name the version messages interpolate
// from the profile directory, falling back to the directory's base name for a
// root that is not one of the known harnesses.
func binaryFor(dir string) string {
	base := filepath.Base(dir)
	if b, ok := HarnessBinary[base]; ok {
		return b
	}
	return base
}

func shortSum(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	if s == "" {
		return "(empty)"
	}
	return s
}
