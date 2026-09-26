//go:build !unix

package skillmat

import (
	"strings"
	"testing"
)

func TestOpenSecureRootFailsClosedOnUnsupportedPlatform(t *testing.T) {
	root, err := openSecureRoot(t.TempDir())
	if root != nil {
		_ = root.Close()
		t.Fatal("openSecureRoot returned a writable root on an unsupported platform")
	}
	if err == nil || !strings.Contains(err.Error(), "skill materialization is not supported on this platform") {
		t.Fatalf("openSecureRoot error = %v, want unsupported-platform refusal", err)
	}
}

func TestMaterializeFailsClosedOnUnsupportedPlatform(t *testing.T) {
	err := materialize(t.Context(), nil, "WS", "lead", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "skill materialization is not supported on this platform") {
		t.Fatalf("Materialize error = %v, want unsupported-platform refusal", err)
	}
}
