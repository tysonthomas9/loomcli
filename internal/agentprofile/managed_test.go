package agentprofile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decode is the shape both sides of subsetOf arrive in at runtime: whatever
// encoding/json produces. Writing the table in JSON rather than Go literals
// keeps the tests honest about that — numbers are float64 on both sides only
// because they went through the decoder.
func decode(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("test fixture is not valid JSON: %v", err)
	}
	return v
}

func TestSubsetOf(t *testing.T) {
	tests := []struct {
		name     string
		want     string
		got      string
		ok       bool
		wantPath string // the operator-facing value; asserted on every failure
	}{
		{
			name: "identical",
			want: `{"a":1,"b":"x"}`,
			got:  `{"a":1,"b":"x"}`,
			ok:   true,
		},
		{
			name: "extra top-level key is allowed",
			want: `{"a":1}`,
			got:  `{"a":1,"enabledPlugins":{"x@y":true}}`,
			ok:   true,
		},
		{
			name: "extra nested key is allowed",
			want: `{"permissions":{"defaultMode":"auto"}}`,
			got:  `{"permissions":{"defaultMode":"auto","additionalDirectories":[]}}`,
			ok:   true,
		},
		{
			name: "reordered keys are allowed",
			want: `{"a":1,"b":2,"c":3}`,
			got:  `{"c":3,"b":2,"a":1}`,
			ok:   true,
		},
		{
			name:     "changed scalar",
			want:     `{"permissions":{"defaultMode":"auto"}}`,
			got:      `{"permissions":{"defaultMode":"plan"}}`,
			wantPath: "permissions.defaultMode",
		},
		{
			name:     "removed key",
			want:     `{"disableRemoteControl":true}`,
			got:      `{"model":"opus"}`,
			wantPath: "disableRemoteControl",
		},
		{
			name:     "removed nested key",
			want:     `{"permissions":{"allow":["a"],"defaultMode":"auto"}}`,
			got:      `{"permissions":{"defaultMode":"auto"}}`,
			wantPath: "permissions.allow",
		},
		{
			name:     "array reordered",
			want:     `{"allow":["a","b"]}`,
			got:      `{"allow":["b","a"]}`,
			wantPath: "allow[0]",
		},
		{
			name:     "array appended",
			want:     `{"allow":["a"]}`,
			got:      `{"allow":["a","b"]}`,
			wantPath: "allow",
		},
		{
			name:     "type mismatch at root: object vs array",
			want:     `{"a":1}`,
			got:      `[1,2,3]`,
			wantPath: "",
		},
		{
			name:     "type mismatch nested: object vs scalar",
			want:     `{"permissions":{"defaultMode":"auto"}}`,
			got:      `{"permissions":"auto"}`,
			wantPath: "permissions",
		},
		{
			name: "nested object recursion, three deep",
			want: `{"a":{"b":{"c":1}}}`,
			got:  `{"a":{"b":{"c":1,"d":2},"e":3},"f":4}`,
			ok:   true,
		},
		{
			name:     "nested object recursion reports the full path",
			want:     `{"a":{"b":{"c":1}}}`,
			got:      `{"a":{"b":{"c":2}}}`,
			wantPath: "a.b.c",
		},
		{
			name:     "array element object diverges",
			want:     `{"hooks":[{"type":"command","run":"x"}]}`,
			got:      `{"hooks":[{"type":"command","run":"y"}]}`,
			wantPath: "hooks[0].run",
		},
		{
			name: "1 and 1.0 both decode to float64 and compare equal",
			want: `{"n":1}`,
			got:  `{"n":1.0}`,
			ok:   true,
		},
		{
			name: "empty baseline object passes against anything",
			want: `{}`,
			got:  `{"whatever":true}`,
			ok:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, ok := subsetOf(decode(t, tt.want), decode(t, tt.got))
			if ok != tt.ok {
				t.Fatalf("subsetOf ok = %v, want %v (path %q)", ok, tt.ok, path)
			}
			if !tt.ok && path != tt.wantPath {
				t.Fatalf("subsetOf path = %q, want %q", path, tt.wantPath)
			}
		})
	}
}

// writeManaged builds a profile root carrying one managed file: the pristine
// baseline under .provisioned/ and whatever the "harness" left on disk.
func writeManaged(t *testing.T, rel, baseline, live string) string {
	t.Helper()
	dir := t.TempDir()
	if baseline != "" {
		path := filepath.Join(dir, ProvisionedDirName, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(baseline), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if live != "" {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(live), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestVerifyManaged_NilListIsANoOp(t *testing.T) {
	if err := verifyManaged(t.TempDir(), nil); err != nil {
		t.Fatalf("verifyManaged(nil) = %v, want nil", err)
	}
}

// Edge 8: a hand-edited manifest naming a managed file that was never
// provisioned must never silently pass — it is a provisioning fault.
func TestVerifyManaged_MissingBaselineIsAManifestFault(t *testing.T) {
	dir := writeManaged(t, "settings.json", "", `{"model":"opus"}`)
	err := verifyManaged(dir, []string{"settings.json"})
	if !errors.Is(err, ErrManifestUnreadable) {
		t.Fatalf("verifyManaged = %v, want ErrManifestUnreadable", err)
	}
}

// Edge 9: the harness deleting the live file is drift, not a manifest fault.
// A profile with no settings is not the provisioned profile.
func TestVerifyManaged_DeletedLiveFileIsDrift(t *testing.T) {
	dir := writeManaged(t, "settings.json", `{"model":"opus"}`, "")
	err := verifyManaged(dir, []string{"settings.json"})
	if !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("verifyManaged = %v, want ErrManagedContentDrift", err)
	}
}

func TestVerifyManaged_LiveFileNotJSONIsDrift(t *testing.T) {
	dir := writeManaged(t, "settings.json", `{"model":"opus"}`, "not json at all")
	err := verifyManaged(dir, []string{"settings.json"})
	if !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("verifyManaged = %v, want ErrManagedContentDrift", err)
	}
	if !strings.Contains(err.Error(), "on-disk file is not valid JSON") {
		t.Fatalf("error does not say WHICH file is unparseable: %v", err)
	}
}

func TestVerifyManaged_BaselineNotJSONIsDrift(t *testing.T) {
	dir := writeManaged(t, "settings.json", "not json at all", `{"model":"opus"}`)
	err := verifyManaged(dir, []string{"settings.json"})
	if !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("verifyManaged = %v, want ErrManagedContentDrift", err)
	}
	if !strings.Contains(err.Error(), "provisioned baseline is not valid JSON") {
		t.Fatalf("error does not say WHICH file is unparseable: %v", err)
	}
}

// Edge 10: a live file that is valid JSON but the wrong SHAPE must be reported
// as drift, not panic.
func TestVerifyManaged_LiveTopLevelArrayIsDrift(t *testing.T) {
	dir := writeManaged(t, "settings.json", `{"model":"opus"}`, `["opus"]`)
	err := verifyManaged(dir, []string{"settings.json"})
	if !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("verifyManaged = %v, want ErrManagedContentDrift", err)
	}
	if !strings.Contains(err.Error(), "(document root)") {
		t.Fatalf("root divergence should be named, got: %v", err)
	}
}

// Edge 11: nothing was provisioned to protect, so nothing can drift.
func TestVerifyManaged_EmptyBaselinePasses(t *testing.T) {
	dir := writeManaged(t, "settings.json", `{}`, `{"anything":true}`)
	if err := verifyManaged(dir, []string{"settings.json"}); err != nil {
		t.Fatalf("verifyManaged = %v, want nil", err)
	}
}

// Edge 12: the harness rewriting a whole object, preserving every provisioned
// key and adding its own, is the INTENDED tolerance. This test exists so a
// later "tighten it up" refactor has to argue with a name.
func TestVerifyManaged_HarnessRewritingAnObjectAroundProvisionedKeysPasses(t *testing.T) {
	dir := writeManaged(t, "settings.json",
		`{"permissions":{"defaultMode":"auto","allow":["Bash"]}}`,
		`{"permissions":{"allow":["Bash"],"additionalDirectories":["/tmp"],"defaultMode":"auto"},"enabledPlugins":{}}`)
	if err := verifyManaged(dir, []string{"settings.json"}); err != nil {
		t.Fatalf("verifyManaged = %v, want nil", err)
	}
}

// The error's dotted path is the operator-facing value: it is what turns "your
// profile drifted" into "someone changed permissions.defaultMode".
func TestVerifyManaged_ErrorNamesTheDivergingPathAndBothValues(t *testing.T) {
	dir := writeManaged(t, "settings.json",
		`{"permissions":{"defaultMode":"auto"}}`,
		`{"permissions":{"defaultMode":"plan"}}`)
	err := verifyManaged(dir, []string{"settings.json"})
	if err == nil {
		t.Fatal("verifyManaged = nil, want drift")
	}
	for _, want := range []string{"settings.json", "permissions.defaultMode", `provisioned "auto"`, `on disk "plan"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

// Edge 15: Python's ensure_ascii escapes non-ASCII where Go emits raw UTF-8.
// Post-parse the two are the same string, which is the whole reason the
// semantic check is immune to a re-serialization the byte hash is not.
func TestVerifyManaged_UnicodeEscapingDoesNotMatterAfterParsing(t *testing.T) {
	// The baseline is written the way Python's json.dump(ensure_ascii=True)
	// writes it; the live file the way Go (or a re-serializing harness) does.
	dir := writeManaged(t, "settings.json", `{"note":"\u00e9t\u00e9"}`, `{"note":"été"}`)
	if err := verifyManaged(dir, []string{"settings.json"}); err != nil {
		t.Fatalf("verifyManaged = %v, want nil", err)
	}
}

// ── TOML managed content ─────────────────────────────────────────────────────
//
// The codex half of the mechanism. codexBaselineTOML is the shape of the real
// profiles/lead/codex/config.toml: the model settings plus the [projects…]
// tables whose trust_level entries are the profile's entire reason to exist.

const codexBaselineTOML = `model = "gpt-5.6-sol"
model_reasoning_effort = "medium"

[projects."/Users/oleh/.loom/workspaces/puppet"]
trust_level = "trusted"

[projects."/Users/oleh/.loom/workspaces/puppet/lead"]
trust_level = "trusted"

[projects."/Users/oleh/.loom/workspaces/PUPPET"]
trust_level = "trusted"

[projects."/Users/oleh/.loom/workspaces/PUPPET/lead"]
trust_level = "trusted"
`

// What codex appends to config.toml on essentially every run. This is the
// ticket: byte-hashed, these two tables brick the next `loom lead` — for the
// claude backend too, since enforceLeadProfile exits on the first harness that
// fails.
const codexRuntimeAppendTOML = `
[hooks.state]

[hooks.state."/Users/oleh/.loom/workspaces/puppet/.codex/hooks.json:user_prompt_submit:0:0"]
trusted_hash = "sha256:8f035f2a"

[tui.model_availability_nux]
gpt-6-astra = 1
`

func TestVerifyManaged_TOMLBaselineIsASubsetOfTheDriftedLiveFile(t *testing.T) {
	dir := writeManaged(t, "config.toml", codexBaselineTOML, codexBaselineTOML+codexRuntimeAppendTOML)
	if err := verifyManaged(dir, []string{"config.toml"}); err != nil {
		t.Fatalf("verifyManaged = %v, want nil: codex's own runtime tables must not be drift", err)
	}
}

// The trust boundary, still enforced. This is the whole justification for
// verifying config.toml semantically instead of unlisting it: a tampered
// trust_level must still refuse, appends or no appends.
func TestVerifyManaged_TOMLDriftWhenTrustLevelChanges(t *testing.T) {
	live := strings.Replace(codexBaselineTOML+codexRuntimeAppendTOML,
		"[projects.\"/Users/oleh/.loom/workspaces/puppet\"]\ntrust_level = \"trusted\"",
		"[projects.\"/Users/oleh/.loom/workspaces/puppet\"]\ntrust_level = \"untrusted\"", 1)
	dir := writeManaged(t, "config.toml", codexBaselineTOML, live)
	err := verifyManaged(dir, []string{"config.toml"})
	if !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("verifyManaged = %v, want ErrManagedContentDrift", err)
	}
	// The path is the operator-facing contract, and it must name the exact
	// project whose trust changed.
	if !strings.Contains(err.Error(), `projects./Users/oleh/.loom/workspaces/puppet.trust_level`) {
		t.Fatalf("error must name the diverging path, got: %v", err)
	}
	// Known limitation, PRE-DATING this change and deliberately not widened
	// here: the dotted path is a string, so valueAt cannot walk back into a key
	// that itself contains dots or slashes — every [projects."…"] key does — and
	// both sides render "(absent)". The path names the divergence correctly,
	// which is what the contract promises; quoting the two values would need
	// subsetOf to carry them out structurally. Pinned so the day someone fixes
	// that, this test tells them the codex case is the reason to.
	if !strings.Contains(err.Error(), "provisioned (absent), on disk (absent)") {
		t.Fatalf("dotted-key rendering changed; if values are now quoted, tighten this test: %v", err)
	}
}

// Dropping a provisioned table is drift even though everything left is
// unchanged: containment is one-directional.
func TestVerifyManaged_TOMLDriftWhenAProvisionedTableIsRemoved(t *testing.T) {
	live := strings.Replace(codexBaselineTOML,
		"[projects.\"/Users/oleh/.loom/workspaces/PUPPET/lead\"]\ntrust_level = \"trusted\"\n", "", 1)
	dir := writeManaged(t, "config.toml", codexBaselineTOML, live)
	if err := verifyManaged(dir, []string{"config.toml"}); !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("verifyManaged = %v, want ErrManagedContentDrift", err)
	}
}

func TestVerifyManaged_LiveFileNotTOMLIsDrift(t *testing.T) {
	dir := writeManaged(t, "config.toml", codexBaselineTOML, `{"model":"gpt-5.6-sol"}`)
	err := verifyManaged(dir, []string{"config.toml"})
	if !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("verifyManaged = %v, want ErrManagedContentDrift", err)
	}
	if !strings.Contains(err.Error(), "on-disk file is not valid TOML") {
		t.Fatalf("error must say WHICH file and WHICH format, got: %v", err)
	}
}

func TestVerifyManaged_BaselineNotTOMLIsDrift(t *testing.T) {
	dir := writeManaged(t, "config.toml", `{"model":"gpt-5.6-sol"}`, codexBaselineTOML)
	err := verifyManaged(dir, []string{"config.toml"})
	if !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("verifyManaged = %v, want ErrManagedContentDrift", err)
	}
	if !strings.Contains(err.Error(), "provisioned baseline is not valid TOML") {
		t.Fatalf("error must say WHICH file and WHICH format, got: %v", err)
	}
}

// A manifest naming a format the verifier cannot read is a PROVISIONING fault,
// not agent drift: re-provisioning alone would not repair it, so it must not be
// reported as something re-provisioning fixes.
func TestVerifyManaged_UnsupportedExtensionIsAManifestFault(t *testing.T) {
	dir := writeManaged(t, "config.yaml", "model: gpt\n", "model: gpt\n")
	err := verifyManaged(dir, []string{"config.yaml"})
	if !errors.Is(err, ErrManifestUnreadable) {
		t.Fatalf("verifyManaged = %v, want ErrManifestUnreadable", err)
	}
	if !strings.Contains(err.Error(), `".yaml"`) {
		t.Fatalf("error must quote the extension it cannot handle, got: %v", err)
	}
}

// A file with no extension at all must not fall through permissively either.
func TestVerifyManaged_NoExtensionIsAManifestFault(t *testing.T) {
	dir := writeManaged(t, "config", "anything", "anything")
	if err := verifyManaged(dir, []string{"config"}); !errors.Is(err, ErrManifestUnreadable) {
		t.Fatalf("verifyManaged = %v, want ErrManifestUnreadable", err)
	}
}

// The host filesystem is case-insensitive; the extension match must be too, or
// a CONFIG.TOML would read as an unsupported format.
func TestVerifyManaged_ExtensionMatchIsCaseInsensitive(t *testing.T) {
	dir := writeManaged(t, "CONFIG.TOML", codexBaselineTOML, codexBaselineTOML+codexRuntimeAppendTOML)
	if err := verifyManaged(dir, []string{"CONFIG.TOML"}); err != nil {
		t.Fatalf("verifyManaged = %v, want nil", err)
	}
}

// go-toml decodes an empty document to an EMPTY MAP, not nil, so a truncated
// config.toml is caught by ordinary containment: every provisioned key is
// missing. Pinned because a nil there would make the comparison vacuous.
func TestVerifyManaged_TruncatedTOMLLiveFileIsDrift(t *testing.T) {
	dir := writeManaged(t, "config.toml", codexBaselineTOML, "\n")
	if err := verifyManaged(dir, []string{"config.toml"}); !errors.Is(err, ErrManagedContentDrift) {
		t.Fatalf("verifyManaged = %v, want ErrManagedContentDrift", err)
	}
}

// Same semantics as the JSON case: nothing was provisioned, so nothing drifts.
func TestVerifyManaged_EmptyTOMLBaselinePasses(t *testing.T) {
	dir := writeManaged(t, "config.toml", "\n", codexBaselineTOML)
	if err := verifyManaged(dir, []string{"config.toml"}); err != nil {
		t.Fatalf("verifyManaged = %v, want nil", err)
	}
}

// The int64/float64 regression guard, and the reason decodeManaged takes `rel`
// rather than being called once per side with whatever format is handy.
// encoding/json decodes 1 to float64; go-toml decodes it to int64. Each
// document verifies against its OWN baseline, but the two decoders' outputs do
// not compare — so a future refactor that "helpfully" normalizes one side, or
// decodes the two sides differently, fails here instead of in production.
func TestVerifyManaged_DecodersAreNotMixed(t *testing.T) {
	jsonDir := writeManaged(t, "settings.json", `{"x":1}`, `{"x":1,"extra":true}`)
	if err := verifyManaged(jsonDir, []string{"settings.json"}); err != nil {
		t.Fatalf("json against json = %v, want nil", err)
	}
	tomlDir := writeManaged(t, "config.toml", "x = 1\n", "x = 1\nextra = true\n")
	if err := verifyManaged(tomlDir, []string{"config.toml"}); err != nil {
		t.Fatalf("toml against toml = %v, want nil", err)
	}

	fromJSON, err := decodeManaged("settings.json", []byte(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	fromTOML, err := decodeManaged("config.toml", []byte("x = 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := subsetOf(fromJSON, fromTOML); ok {
		t.Fatal("json-decoded and toml-decoded numbers compared equal: " +
			"a decoder was normalized, and the same-decoder invariant is gone")
	}
}
