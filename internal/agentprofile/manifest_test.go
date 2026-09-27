package agentprofile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const testVersion = "2.1.234 (Claude Code)"

// writeProfile materializes a harness profile root under dir's parent and
// writes a 0644 manifest over the given files, mirroring what
// provision-profile.sh produces.
func writeProfile(t *testing.T, root, harness, version string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, harness)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(files[name]), 0o600); err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(files[name]))
	}
	raw, err := json.Marshal(Manifest{
		Files:          names,
		Fingerprint:    hex.EncodeToString(h.Sum(nil)),
		HarnessVersion: version,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), raw, 0o644); err != nil { //nolint:gosec // G306: manifests are world-readable by design, as the provisioner writes them
		t.Fatal(err)
	}
	return dir
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled path
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestVerify_WellFormedProfilePasses(t *testing.T) {
	dir := writeProfile(t, t.TempDir(), "claude", testVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
		"CLAUDE.md":     "house rules\n",
	})
	if err := Verify(dir, testVersion); err != nil {
		t.Fatalf("well-formed profile must verify: %v", err)
	}
}

// The fingerprint scheme is pinned against an independent computation, not
// against the code under test: a second implementation of it exists in the
// operator's provisioner, and the two must never drift.
func TestFingerprint_MatchesHandComputedSum(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("beta"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("a.txt\x00alpha" + "b.txt\x00beta"))
	got, err := Fingerprint(dir, []string{"a.txt", "b.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if got != hex.EncodeToString(want[:]) {
		t.Errorf("fingerprint = %s, want %s", got, hex.EncodeToString(want[:]))
	}
}

func TestFingerprint_IgnoresUnlistedFiles(t *testing.T) {
	root := t.TempDir()
	dir := writeProfile(t, root, "claude", testVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	// The harness owns these and rewrites them at runtime; the manifest is an
	// allowlist precisely so that churn is not a boot failure.
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dir, testVersion); err != nil {
		t.Fatalf("unlisted files must not affect verification: %v", err)
	}
}

func TestVerify_Sentinels(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, dir string)
		gotVersion string
		want       error
	}{
		{
			name:       "missing manifest",
			setup:      func(t *testing.T, dir string) { rm(t, filepath.Join(dir, ManifestName)) },
			gotVersion: testVersion,
			want:       ErrManifestMissing,
		},
		{
			name: "malformed manifest",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte("{not json"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			gotVersion: testVersion,
			want:       ErrManifestUnreadable,
		},
		{
			name:       "listed file deleted",
			setup:      func(t *testing.T, dir string) { rm(t, filepath.Join(dir, "settings.json")) },
			gotVersion: testVersion,
			want:       ErrManifestUnreadable,
		},
		{
			name: "listed file edited",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"model":"tampered"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			gotVersion: testVersion,
			want:       ErrFingerprintMismatch,
		},
		{
			name:       "version drift",
			setup:      func(t *testing.T, dir string) {},
			gotVersion: "2.2.0 (Claude Code)",
			want:       ErrVersionDrift,
		},
		{
			name:       "version unknown",
			setup:      func(t *testing.T, dir string) {},
			gotVersion: "",
			want:       ErrVersionUnknown,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeProfile(t, t.TempDir(), "claude", testVersion, map[string]string{
				"settings.json": `{"model":"opus"}`,
			})
			tc.setup(t, dir)
			err := Verify(dir, tc.gotVersion)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Verify = %v, want %v", err, tc.want)
			}
		})
	}
}

// Operators match on this wording, and the binary name in it is derived from
// the directory rather than passed in — so the rendered string is the contract.
func TestVerify_MessageWording(t *testing.T) {
	root := t.TempDir()
	dir := writeProfile(t, root, "claude", testVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})

	err := Verify(dir, "2.2.0 (Claude Code)")
	want := "profile harness version drift: " + dir +
		`: manifest pins "2.1.234 (Claude Code)", claude reports "2.2.0 (Claude Code)" (re-provision to bless the upgrade)`
	if err == nil || err.Error() != want {
		t.Errorf("drift message =\n%v\nwant\n%s", err, want)
	}

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if werr := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"model":"tampered"}`), 0o600); werr != nil {
		t.Fatal(werr)
	}
	onDisk, err := Fingerprint(dir, m.Files)
	if err != nil {
		t.Fatal(err)
	}
	err = Verify(dir, testVersion)
	want = "profile fingerprint mismatch: " + dir + ": manifest " + shortSum(m.Fingerprint) +
		", on disk " + shortSum(onDisk) + " (re-provision the profile)"
	if err == nil || err.Error() != want {
		t.Errorf("mismatch message =\n%v\nwant\n%s", err, want)
	}
}

func TestBless_UpdatesVersionOnly(t *testing.T) {
	dir := writeProfile(t, t.TempDir(), "claude", testVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	before, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	const upgraded = "2.2.0 (Claude Code)"
	if err := Bless(dir, upgraded); err != nil {
		t.Fatalf("bless on drift must succeed: %v", err)
	}
	after, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after.HarnessVersion != upgraded {
		t.Errorf("harness_version = %q, want %q", after.HarnessVersion, upgraded)
	}
	if !reflect.DeepEqual(after.Files, before.Files) || after.Fingerprint != before.Fingerprint {
		t.Errorf("bless must touch only the version: %+v vs %+v", after, before)
	}
	if err := Verify(dir, upgraded); err != nil {
		t.Errorf("blessed profile must verify: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644 (preserved from the provisioner)", info.Mode().Perm())
	}
	assertNoTempLitter(t, dir)
}

// The manifest is never in its own Files list, so blessing cannot invalidate
// the fingerprint and a second bless is a pure no-op.
func TestBless_IsIdempotent(t *testing.T) {
	dir := writeProfile(t, t.TempDir(), "claude", testVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	const upgraded = "2.2.0 (Claude Code)"
	if err := Bless(dir, upgraded); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ManifestName)
	first := readFile(t, path)
	if err := Bless(dir, upgraded); err != nil {
		t.Fatalf("second bless must be a no-op, got %v", err)
	}
	if got := readFile(t, path); string(got) != string(first) {
		t.Errorf("no-op bless rewrote the file:\n%s\nvs\n%s", got, first)
	}
	if err := Verify(dir, upgraded); err != nil {
		t.Errorf("verify after double bless: %v", err)
	}
	assertNoTempLitter(t, dir)
}

func TestBless_RefusesAndWritesNothing(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, dir string)
		gotVersion string
		want       error
	}{
		{
			name: "fingerprint mismatch",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"model":"tampered"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			gotVersion: "2.2.0 (Claude Code)",
			want:       ErrFingerprintMismatch,
		},
		{
			name:       "unknown version",
			setup:      func(t *testing.T, dir string) {},
			gotVersion: "",
			want:       ErrVersionUnknown,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeProfile(t, t.TempDir(), "claude", testVersion, map[string]string{
				"settings.json": `{"model":"opus"}`,
			})
			path := filepath.Join(dir, ManifestName)
			before := readFile(t, path)
			tc.setup(t, dir)

			if err := Bless(dir, tc.gotVersion); !errors.Is(err, tc.want) {
				t.Fatalf("Bless = %v, want %v", err, tc.want)
			}
			if got := readFile(t, path); string(got) != string(before) {
				t.Errorf("refused bless must not rewrite the manifest:\n%s", got)
			}
			assertNoTempLitter(t, dir)
		})
	}
}

// Provisioning is not Bless's job: a dir that was never provisioned stays that
// way, rather than acquiring a manifest that attests to nothing.
func TestBless_MissingManifestCreatesNothing(t *testing.T) {
	dir := t.TempDir()
	if err := Bless(dir, testVersion); !errors.Is(err, ErrManifestMissing) {
		t.Fatalf("Bless = %v, want ErrManifestMissing", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ManifestName)); !os.IsNotExist(err) {
		t.Errorf("Bless created a manifest where none existed (stat err %v)", err)
	}
	assertNoTempLitter(t, dir)
}

// Byte-shape parity with provision-profile.sh's json.dump(..., indent=1): one
// space of indent, no HTML escaping, no trailing newline. Without it a later
// full re-provision shows a spurious whole-file diff.
func TestBless_OutputMatchesProvisionerShape(t *testing.T) {
	dir := writeProfile(t, t.TempDir(), "claude", testVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	if err := Bless(dir, "2.2.0 <Claude Code>"); err != nil {
		t.Fatal(err)
	}
	raw := string(readFile(t, filepath.Join(dir, ManifestName)))
	if strings.HasSuffix(raw, "\n") {
		t.Errorf("manifest must not end with a newline:\n%q", raw)
	}
	if !strings.Contains(raw, "\n \"fingerprint\": ") {
		t.Errorf("expected one-space indent, got:\n%s", raw)
	}
	if !strings.Contains(raw, `"2.2.0 <Claude Code>"`) {
		t.Errorf("expected unescaped angle brackets, got:\n%s", raw)
	}
	if !strings.HasPrefix(raw, "{\n \"files\": ") {
		t.Errorf("expected provisioner field order, got:\n%s", raw)
	}
}

func TestList(t *testing.T) {
	projectDir := t.TempDir()

	// Missing agent-profiles root is the default, not an error.
	got, err := List(projectDir)
	if err != nil || got != nil {
		t.Fatalf("List on a fleet with no profiles = %v, %v; want nil, nil", got, err)
	}

	root := filepath.Join(projectDir, ".loom", DirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"observer", "worker"} {
		for _, harness := range []string{"claude", "codex"} {
			if err := os.MkdirAll(filepath.Join(root, agent, harness), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Skipped: an agent dir with neither harness root, and a regular file.
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err = List(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Profile{
		{Agent: "observer", Harness: "claude", Dir: filepath.Join(root, "observer", "claude")},
		{Agent: "observer", Harness: "codex", Dir: filepath.Join(root, "observer", "codex")},
		{Agent: "worker", Harness: "claude", Dir: filepath.Join(root, "worker", "claude")},
		{Agent: "worker", Harness: "codex", Dir: filepath.Join(root, "worker", "codex")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List =\n%+v\nwant\n%+v", got, want)
	}
}

// No binary is assumed on PATH — CI has neither claude nor codex.
func TestProbeVersion_NonexistentBinary(t *testing.T) {
	if got := ProbeVersion("loom-no-such-harness-binary", time.Second); got != "" {
		t.Errorf("ProbeVersion on a missing binary = %q, want \"\"", got)
	}
}

func assertNoTempLitter(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ManifestName+".tmp-") {
			t.Errorf("temp file left behind in the profile root: %s", e.Name())
		}
	}
}

func rm(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// ── managed content (the .provisioned baseline) ─────────────────────────────

// writeManagedProfile materializes what the provisioner produces for a profile
// with one managed file: the live copy (NOT hashed, NOT in `files`), the
// pristine baseline under .provisioned/ (hashed, IS in `files`), plus whatever
// ordinary byte-hashed files the caller asks for.
func writeManagedProfile(t *testing.T, root, version string, files map[string]string, rel, baseline, live string) string {
	t.Helper()
	return writeManagedProfileFor(t, root, "claude", version, files, rel, baseline, live)
}

// writeManagedProfileFor is writeManagedProfile with the harness subdirectory
// named, because the managed scheme is not claude-only any more: codex's
// config.toml is the file that made it format-aware.
func writeManagedProfileFor(t *testing.T, root, harness, version string, files map[string]string, rel, baseline, live string) string {
	t.Helper()
	all := map[string]string{}
	for k, v := range files {
		all[k] = v
	}
	all[filepath.Join(ProvisionedDirName, rel)] = baseline

	dir := filepath.Join(root, harness)
	if err := os.MkdirAll(filepath.Join(dir, ProvisionedDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(all[name]), 0o600); err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(all[name]))
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(Manifest{
		Files:          names,
		Managed:        []string{rel},
		Fingerprint:    hex.EncodeToString(h.Sum(nil)),
		HarnessVersion: version,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), raw, 0o644); err != nil { //nolint:gosec // G306: manifests are world-readable by design, as the provisioner writes them
		t.Fatal(err)
	}
	return dir
}

// Every profile on disk today has no "managed" key. It must unmarshal to nil,
// verifyManaged must be a no-op, and settings.json must stay byte-hashed —
// this is the regression guard for the whole existing fleet.
func TestVerify_ManifestWithoutManagedKeepsByteBehavior(t *testing.T) {
	dir := writeProfile(t, t.TempDir(), "claude", testVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Managed != nil {
		t.Fatalf("Managed = %v, want nil for a manifest with no managed key", m.Managed)
	}
	if err := Verify(dir, testVersion); err != nil {
		t.Fatalf("Verify = %v, want nil", err)
	}
	// The byte hash is still in force for it: one edited character refuses.
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dir, testVersion); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("Verify = %v, want ErrFingerprintMismatch", err)
	}
}

// The 2026-08-30 outage, reproduced: Claude Code re-serialized worker's
// settings.json with a different key order and added "enabledPlugins", the byte
// hash tripped, and the agent became permanently unspawnable because Bless
// refuses on a fingerprint mismatch. Enabling a plugin bricked an agent.
// Under the managed scheme this is a clean pass.
func TestVerify_ManagedSurvivesTheKeyReorderPlusEnabledPluginsOutage(t *testing.T) {
	dir := writeManagedProfile(t, t.TempDir(), testVersion,
		map[string]string{"CLAUDE.md": "house rules\n"},
		"settings.json",
		`{"permissions":{"defaultMode":"auto"},"disableRemoteControl":true}`,
		`{"disableRemoteControl":true,"enabledPlugins":{"x@y":true},"permissions":{"defaultMode":"auto"}}`)
	if err := Verify(dir, testVersion); err != nil {
		t.Fatalf("Verify = %v, want nil: the harness reordering keys and adding a runtime key must not brick the agent", err)
	}
}

func TestVerify_ManagedDriftWhenAProvisionedKeyChanges(t *testing.T) {
	dir := writeManagedProfile(t, t.TempDir(), testVersion, nil, "settings.json",
		`{"permissions":{"defaultMode":"auto"}}`,
		`{"permissions":{"defaultMode":"plan"}}`)
	err := Verify(dir, testVersion)
	if !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("Verify = %v, want ErrManagedContentDrift", err)
	}
	if !strings.Contains(err.Error(), "permissions.defaultMode") {
		t.Fatalf("error does not name the diverging path: %v", err)
	}
}

func TestVerify_ManagedDriftWhenAProvisionedKeyIsRemoved(t *testing.T) {
	dir := writeManagedProfile(t, t.TempDir(), testVersion, nil, "settings.json",
		`{"disableRemoteControl":true}`,
		`{"enabledPlugins":{}}`)
	if err := Verify(dir, testVersion); !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("Verify = %v, want ErrManagedContentDrift", err)
	}
}

// A hand-edited manifest naming a managed file with no .provisioned/ baseline
// is a provisioning fault, not drift, and must never silently pass.
func TestVerify_ManagedWithNoBaselineIsUnreadable(t *testing.T) {
	dir := writeProfile(t, t.TempDir(), "claude", testVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Managed = []string{"settings.json"}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), raw, 0o644); err != nil { //nolint:gosec // G306: matches the provisioner's mode
		t.Fatal(err)
	}
	if err := Verify(dir, testVersion); !errors.Is(err, ErrManifestUnreadable) {
		t.Fatalf("Verify = %v, want ErrManifestUnreadable", err)
	}
}

// Edge 16: content faults outrank version faults. A profile with BOTH managed
// drift and a same-major version drift must report the content fault, so the
// soft-failing version drift can never mask a real content change.
func TestVerify_ManagedDriftBeatsVersionDrift(t *testing.T) {
	dir := writeManagedProfile(t, t.TempDir(), "2.1.234 (Claude Code)", nil, "settings.json",
		`{"permissions":{"defaultMode":"auto"}}`,
		`{"permissions":{"defaultMode":"plan"}}`)
	err := Verify(dir, "2.1.251 (Claude Code)")
	if !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("Verify = %v, want ErrManagedContentDrift to outrank ErrVersionDrift", err)
	}
	if errors.Is(err, ErrVersionDrift) {
		t.Fatalf("Verify reported version drift, hiding the content fault: %v", err)
	}
}

// Bless must never launder unverified content. It already refused on a
// fingerprint mismatch; managed drift is the same property under the semantic
// scheme.
func TestBless_RefusesOnManagedDrift(t *testing.T) {
	dir := writeManagedProfile(t, t.TempDir(), "2.1.234 (Claude Code)", nil, "settings.json",
		`{"permissions":{"defaultMode":"auto"}}`,
		`{"permissions":{"defaultMode":"plan"}}`)
	before := readFile(t, filepath.Join(dir, ManifestName))
	if err := Bless(dir, "2.1.251 (Claude Code)"); !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("Bless = %v, want ErrManagedContentDrift", err)
	}
	if after := readFile(t, filepath.Join(dir, ManifestName)); string(after) != string(before) {
		t.Fatal("Bless rewrote the manifest despite refusing")
	}
}

// The other half: a managed profile whose live file merely gained a runtime key
// still blesses, because it verifies.
func TestBless_SucceedsWhenManagedContentOnlyGainedKeys(t *testing.T) {
	dir := writeManagedProfile(t, t.TempDir(), "2.1.234 (Claude Code)", nil, "settings.json",
		`{"permissions":{"defaultMode":"auto"}}`,
		`{"enabledPlugins":{"x@y":true},"permissions":{"defaultMode":"auto"}}`)
	if err := Bless(dir, "2.1.251 (Claude Code)"); err != nil {
		t.Fatalf("Bless = %v, want nil", err)
	}
	if err := Verify(dir, "2.1.251 (Claude Code)"); err != nil {
		t.Fatalf("Verify after Bless = %v, want nil", err)
	}
}

// marshalManifest reproduces the provisioner's json.dump(..., indent=1)
// byte-for-byte, and that has to keep holding now that `managed` sits between
// `files` and `fingerprint`.
func TestMarshalManifest_ManagedFieldOrderMatchesTheProvisioner(t *testing.T) {
	raw, err := marshalManifest(Manifest{
		Files:          []string{".provisioned/settings.json", "CLAUDE.md"},
		Managed:        []string{"settings.json"},
		Fingerprint:    "abc",
		HarnessVersion: testVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n \"files\": [\n  \".provisioned/settings.json\",\n  \"CLAUDE.md\"\n ],\n \"managed\": [\n  \"settings.json\"\n ],\n \"fingerprint\": \"abc\",\n \"harness_version\": \"" + testVersion + "\"\n}"
	if string(raw) != want {
		t.Fatalf("marshalManifest =\n%s\nwant\n%s", raw, want)
	}
}

// A manifest with no managed key must not grow one when re-marshaled, or every
// existing profile would diff against a fresh provision after one Bless.
func TestMarshalManifest_OmitsManagedWhenAbsent(t *testing.T) {
	raw, err := marshalManifest(Manifest{
		Files:          []string{"CLAUDE.md"},
		Fingerprint:    "abc",
		HarnessVersion: testVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "managed") {
		t.Fatalf("marshalManifest emitted a managed key for a manifest that has none:\n%s", raw)
	}
}

// The codex outage, end to end through Verify. `loom lead` refused to boot
// because config.toml was byte-hashed and codex appends [hooks.state] and
// [tui.model_availability_nux] to it on essentially every run — and since
// enforceLeadProfile walks every harness and exits on the first failure, the
// drifted CODEX profile also blocked `loom lead --backend claude`. With
// config.toml moved out of `files` and into `managed`, the append is a pass.
func TestVerify_CodexConfigTOMLSurvivesTheHooksStateAppend(t *testing.T) {
	const codexVersion = "codex-cli 0.153.2"
	dir := writeManagedProfileFor(t, t.TempDir(), "codex", codexVersion, nil, "config.toml",
		codexBaselineTOML, codexBaselineTOML+codexRuntimeAppendTOML)

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The manifest shape the provisioner must produce: the BASELINE is hashed,
	// the live config.toml is not in `files` at all.
	if got := m.Managed; len(got) != 1 || got[0] != "config.toml" {
		t.Fatalf("Managed = %v, want [config.toml]", got)
	}
	for _, f := range m.Files {
		if f == "config.toml" {
			t.Fatal("config.toml must not be byte-hashed once it is managed")
		}
	}
	if err := Verify(dir, codexVersion); err != nil {
		t.Fatalf("Verify = %v, want nil: codex's own runtime tables must not brick the lead", err)
	}

	// And the trust boundary still holds through the same path.
	tampered := strings.Replace(codexBaselineTOML+codexRuntimeAppendTOML,
		`trust_level = "trusted"`, `trust_level = "untrusted"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dir, codexVersion); !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("Verify = %v, want ErrManagedContentDrift", err)
	}
}

// Bless shares verifyManaged, so the repair path has to be unblocked too:
// re-blessing a codex profile whose config.toml only gained tables must succeed
// rather than refuse the way the byte scheme did.
func TestBless_SucceedsWhenCodexConfigOnlyGainedTables(t *testing.T) {
	dir := writeManagedProfileFor(t, t.TempDir(), "codex", "codex-cli 0.153.2", nil, "config.toml",
		codexBaselineTOML, codexBaselineTOML+codexRuntimeAppendTOML)
	if err := Bless(dir, "codex-cli 0.154.0"); err != nil {
		t.Fatalf("Bless = %v, want nil", err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.HarnessVersion != "codex-cli 0.154.0" {
		t.Fatalf("HarnessVersion = %q, want the blessed version", m.HarnessVersion)
	}
	if got := m.Managed; len(got) != 1 || got[0] != "config.toml" {
		t.Fatalf("Bless dropped the managed list: %v", got)
	}
}
