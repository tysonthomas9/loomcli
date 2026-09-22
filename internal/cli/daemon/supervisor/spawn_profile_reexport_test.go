package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/harnessprofile"
)

// The policy moved to internal/harnessprofile and its suite moved with it.
// What still has to hold HERE is that the supervisor's exported names are the
// same thing and not a second copy: the sentinels must be identical values
// (errors.Is is used across packages) and AppendProfileEnv must reach the
// shared implementation.

func TestProfileSentinelsAreTheSharedValues(t *testing.T) {
	for _, pair := range []struct {
		name             string
		here, harnesspkg error
	}{
		{"manifest missing", ErrProfileManifestMissing, harnessprofile.ErrProfileManifestMissing},
		{"manifest unreadable", ErrProfileManifestUnreadable, harnessprofile.ErrProfileManifestUnreadable},
		{"fingerprint mismatch", ErrProfileFingerprintMismatch, harnessprofile.ErrProfileFingerprintMismatch},
		{"version drift", ErrProfileVersionDrift, harnessprofile.ErrProfileVersionDrift},
		{"version unknown", ErrProfileVersionUnknown, harnessprofile.ErrProfileVersionUnknown},
		{"token unreadable", ErrProfileTokenUnreadable, harnessprofile.ErrProfileTokenUnreadable},
	} {
		if !errors.Is(pair.here, pair.harnesspkg) {
			t.Errorf("%s: supervisor sentinel is a copy, not the shared value", pair.name)
		}
	}
}

func TestAppendProfileEnvReachesTheSharedImplementation(t *testing.T) {
	projectDir := t.TempDir()
	// An unprovisioned root: the shared implementation refuses it, a local
	// stub that forgot to verify would not.
	dir := filepath.Join(projectDir, ".loom", agentprofile.DirName, "nova", "claude")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	env, err := AppendProfileEnv(nil, projectDir, "nova")
	if !errors.Is(err, ErrProfileManifestMissing) {
		t.Fatalf("want missing manifest, got %v (env %v)", err, env)
	}
}

func TestProfileTablesReExportedIntact(t *testing.T) {
	harnesses := ProfileHarnesses()
	if len(harnesses) == 0 {
		t.Fatal("no harnesses re-exported")
	}
	for _, harness := range harnesses {
		if got := ProfileEnvVar(harness); got != harnessprofile.ProfileEnvVar(harness) || got == "" {
			t.Errorf("harness %q env var = %q", harness, got)
		}
		if got := ProfileHarnessBinary(harness); got != harnessprofile.ProfileHarnessBinary(harness) || got == "" {
			t.Errorf("harness %q binary = %q", harness, got)
		}
	}
	if !strings.HasSuffix(AgentProfilesDirName, agentprofile.DirName) {
		t.Errorf("AgentProfilesDirName = %q", AgentProfilesDirName)
	}
	if ProfileManifestName != agentprofile.ManifestName {
		t.Errorf("ProfileManifestName = %q", ProfileManifestName)
	}
}
