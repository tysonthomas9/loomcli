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
