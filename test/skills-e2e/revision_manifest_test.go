//go:build e2e

package skillse2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

var exactProviderRevisionManifest = registry.Scenario{
	ID:       "exact-provider-revision-manifest",
	Behavior: "provider evidence binds the exact Loom, Fleet, corpus, backend, and provider revisions",
	Cases:    []registry.EdgeCase{{ID: 81}},
}

func TestCompatibilityRevisionManifestMatchesProviderRun(t *testing.T) {
	backend, provider, err := registry.RuntimeCoordinatesFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if provider != registry.ProviderMinIO && provider != registry.ProviderGCS {
		t.Skip("revision-manifest evidence belongs to MinIO and GCS provider runs")
	}
	exactProviderRevisionManifest.Covers(t)

	manifestPath := strings.TrimSpace(os.Getenv("SKILLS_E2E_REVISION_MANIFEST"))
	if manifestPath == "" {
		t.Fatal("SKILLS_E2E_REVISION_MANIFEST is required for provider evidence")
	}
	// #nosec G304 -- CI supplies the run-owned evidence path.
	manifest, err := os.Open(manifestPath)
	if err != nil {
		t.Fatalf("open compatibility revision manifest: %v", err)
	}
	defer manifest.Close() //nolint:errcheck

	loomRoot := loomRepositoryRoot(t)
	want := registry.CompatibilityRevisionManifest{
		LoomRevision:   gitRevision(t, loomRoot),
		FleetRevision:  gitRevision(t, requiredDirectory(t, "FLEET_DB_REPO")),
		CorpusRevision: gitRevision(t, requiredDirectory(t, "VERCEL_SKILLS_REPO")),
		Backend:        backend,
		Provider:       provider,
	}
	if edgeRevision := os.Getenv("SKILLS_EDGE_REVISION"); edgeRevision != want.LoomRevision {
		t.Fatalf("SKILLS_EDGE_REVISION = %q, want checked-out Loom revision %q", edgeRevision, want.LoomRevision)
	}
	if err := registry.ValidateCompatibilityRevisionManifest(manifest, want); err != nil {
		t.Fatal(err)
	}
}

func loomRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate compatibility test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func requiredDirectory(t *testing.T, name string) string {
	t.Helper()
	path := strings.TrimSpace(os.Getenv(name))
	if path == "" {
		t.Fatalf("%s is required for provider evidence", name)
	}
	return path
}

func gitRevision(t *testing.T, repository string) string {
	t.Helper()
	// #nosec G204 -- repository is one explicit CI checkout path, never shell text.
	output, err := exec.Command("git", "-C", repository, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("read Git revision for %s: %v", repository, err)
	}
	return strings.TrimSpace(string(output))
}
