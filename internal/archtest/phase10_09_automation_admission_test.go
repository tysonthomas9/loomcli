package archtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase1009GenericAutomationAdmissionEnvelopeCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	repository := openRootedTestFS(t, root)
	forbidden := []string{
		"type EventAuthority struct",
		"type AdmitEventCommand struct",
		"type EventAdmission interface",
		"func NewWebhookEventAuthority(",
		"func NewExecutionEventAuthority(",
		"func NewSystemEventAuthority(",
		"func (s *Service) AdmitEvent(",
	}
	if err := fs.WalkDir(repository, "internal", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := fs.ReadFile(repository, path)
		if readErr != nil {
			return readErr
		}
		for _, fragment := range forbidden {
			if strings.Contains(string(content), fragment) {
				t.Errorf("retired generic Automation admission seam %q returned in %s", fragment, path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPhase1009OriginPortsStayBoundToTheirNamedAdapters(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]string{
		"AdmitWebhookEvent(":  "internal/app/webhookingestion/workflow.go",
		"AdmitWorkflowEvent(": "internal/app/workfloweventing/workflow.go",
		"AdmitSystemEvent(":   "internal/app/systemeventing/workflow.go",
	}
	for _, path := range productionGoFilesBelow(t, root, "internal") {
		if strings.HasPrefix(path, "internal/modules/automation/") {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		for selector, owner := range allowed {
			if strings.Contains(string(content), selector) && path != owner {
				t.Errorf("%s invokes %s outside its named origin adapter %s", path, selector, owner)
			}
		}
	}
}
