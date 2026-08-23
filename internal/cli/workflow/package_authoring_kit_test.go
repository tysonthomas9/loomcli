package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/workflows/authoringkit"
)

// xtWriteKitSource builds a minimal authoring-kit source tree: one data file
// (tool.js) and one fake, unsigned Mach-O (addon.node, little-endian 64-bit thin
// magic 0xCFFAEDFE). It returns the source dir.
func xtWriteKitSource(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "tool.js"), []byte("export const x = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	macho := append([]byte{0xCF, 0xFA, 0xED, 0xFE}, []byte("\x07\x00\x00\x01fake-macho-body")...)
	if err := os.WriteFile(filepath.Join(src, "addon.node"), macho, 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

type xtManifest struct {
	Files []struct {
		Path   string `json:"path"`
		Kind   string `json:"kind"`
		SHA256 string `json:"sha256"`
		TeamID string `json:"team_id"`
	} `json:"files"`
}

// TestPackageAuthoringKitClassifiesMachO proves the packager classifies each file
// by content: a JS file records as data with a content hash, while an unsigned
// fake Mach-O records as macho, bound by content hash (SHA256 set, TeamID empty)
// because codesign verification fails on it.
func TestPackageAuthoringKitClassifiesMachO(t *testing.T) {
	src := xtWriteKitSource(t)
	out := filepath.Join(t.TempDir(), "kit")
	if err := packageAuthoringKit(out, []string{"pkg=" + src}); err != nil {
		t.Fatalf("packageAuthoringKit: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(out, "kit-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m xtManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v\n%s", err, raw)
	}

	var sawJS, sawMacho bool
	for _, f := range m.Files {
		switch {
		case strings.HasSuffix(f.Path, "tool.js"):
			sawJS = true
			if f.Kind != "data" {
				t.Errorf("tool.js Kind = %q, want data", f.Kind)
			}
			if f.SHA256 == "" {
				t.Errorf("tool.js SHA256 empty, want non-empty")
			}
		case strings.HasSuffix(f.Path, "addon.node"):
			sawMacho = true
			if f.Kind != "macho" {
				t.Errorf("addon.node Kind = %q, want macho", f.Kind)
			}
			if f.SHA256 == "" {
				t.Errorf("addon.node SHA256 empty; unsigned macho must bind by content hash")
			}
			if f.TeamID != "" {
				t.Errorf("addon.node TeamID = %q, want empty for an unsigned macho", f.TeamID)
			}
		}
	}
	if !sawJS {
		t.Errorf("manifest missing tool.js entry: %+v", m.Files)
	}
	if !sawMacho {
		t.Errorf("manifest missing addon.node entry: %+v", m.Files)
	}
}

// TestPackagedKitRoundTripsThroughVerify proves a freshly packaged kit passes the
// fail-closed verifier: data files verify by sha256 and the unsigned macho by
// sha256, so Lookup returns a Kit with no error when pointed at the packaged dir.
func TestPackagedKitRoundTripsThroughVerify(t *testing.T) {
	src := xtWriteKitSource(t)
	out := filepath.Join(t.TempDir(), "kit")
	if err := packageAuthoringKit(out, []string{"pkg=" + src}); err != nil {
		t.Fatalf("packageAuthoringKit: %v", err)
	}

	t.Setenv("LOOM_AUTHORING_KIT_DIR", out)
	authoringkit.ResetForTest()
	t.Cleanup(authoringkit.ResetForTest)

	k, err := authoringkit.Lookup()
	if err != nil {
		t.Fatalf("Lookup err = %v, want nil (packaged kit should verify)", err)
	}
	if k == nil {
		t.Fatalf("Lookup returned nil Kit with nil error")
	}
}
