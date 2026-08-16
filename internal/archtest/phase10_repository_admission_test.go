package archtest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase10RepositoryAdmissionOwnsWorkflowAndProcessLocalJobsStayDeleted(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{
		"internal/cli/serve/workspacemgr",
		"internal/configlock",
		"internal/webui/workspacecoord/job_store.go",
	} {
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(retired))); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("retired repository-admission path %s returned (stat error: %v)", retired, statErr)
		}
	}

	repository := openRootedTestFS(t, root)
	for _, forbidden := range []string{
		"type JobRegistry interface",
		"type WorkspaceJobRegistry struct",
		"NewWorkspaceJobRegistry",
	} {
		if err := fs.WalkDir(repository, "internal", func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || strings.HasSuffix(path, "_test.go") || filepath.Ext(path) != ".go" {
				return nil
			}
			content, readErr := fs.ReadFile(repository, path)
			if readErr != nil {
				return readErr
			}
			if strings.Contains(string(content), forbidden) {
				t.Errorf("retired process-local job authority %q returned in %s", forbidden, path)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPhase10RepositoryAdmissionModuleDoesNotImportDelivery(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "internal", "app", "repositoryadmission"))
	if err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.HasSuffix(path, "_test.go") || filepath.Ext(path) != ".go" {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, forbidden := range []string{
			"internal/cli",
			"internal/domain",
			"internal/infra",
			"internal/ops",
			"internal/store",
			"internal/webui",
		} {
			if strings.Contains(string(content), forbidden) {
				t.Errorf("Repository Admission implementation depends on delivery concern %q: %s", forbidden, path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
