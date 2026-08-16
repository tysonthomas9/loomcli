package archtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase1008LegacyPRReviewerProvisioningCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	legacyPackage := filepath.Join(root, "internal", "app", "prreviewer")
	if _, err := os.Stat(legacyPackage); err == nil {
		t.Fatalf("retired sequential PR reviewer package returned: %s", legacyPackage)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}

	forbidden := []string{
		"internal/app/prreviewer",
		"RoleCommitted",
		"serve-pr-reviewer-provisioning",
		"AuthorityForReviewerRole",
		"AuthorityForReviewerAgent",
	}
	repository := openRootedTestFS(t, root)
	if err := fs.WalkDir(repository, "internal", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := fs.ReadFile(repository, path)
		if err != nil {
			return err
		}
		for _, value := range forbidden {
			if strings.Contains(string(content), value) {
				t.Errorf("retired sequential reviewer seam %q returned in %s", value, path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPhase1008ReviewerUsesOneAtomicFleetRoute(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "internal", "infra", "fleetdb", "agent_management_transport.go"))
	if err != nil {
		t.Fatal(err)
	}
	route := "/managed-reviewer-identities/"
	if count := strings.Count(string(content), route); count != 1 {
		t.Fatalf("atomic managed reviewer Fleet route count = %d, want 1", count)
	}
	if strings.Contains(string(content), "EnsureManagedRole") ||
		strings.Contains(string(content), "EnsureManagedAgent") {
		t.Fatal("managed reviewer Fleet transport regained sequential Role/Agent provisioning")
	}
}
