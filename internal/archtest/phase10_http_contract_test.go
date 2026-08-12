package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase10HandwrittenServerDTOPackageCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	retired := filepath.Join(root, "internal", "webui", "server", "dto")
	walkErr := filepath.WalkDir(retired, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			t.Errorf("retired handwritten server DTO file returned: %s", path)
		}
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		t.Fatal(walkErr)
	}
}

func TestPhase10MigratedHTTPContractsCannotReturnAsHandwrittenStructs(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	retired := []string{
		"agentRunsResponse", "agentSessionTranscriptResponse", "interactivePromptsResponse",
		"createPromptAgentRequest", "patchAgentRecordRequest",
		"fileWriteRequest", "fileRepoRequest", "workerRegisterRequest", "workerStateRequest",
		"tabPatchRequest", "tabPutRequest", "taskWorkflowRunsResponse", "driverRunResponse",
		"pullRequestDetail", "pullRequestDiff", "pullRequestDiffFile", "pullRequestReviewRequest",
		"pullRequestReviewResult", "reviewerConversation", "reviewerEnsureResult",
		"reviewerMessageRequest", "reviewerMessageResult",
	}
	handlers := filepath.Join(root, "internal", "webui", "handlers")
	if err := filepath.WalkDir(handlers, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, name := range retired {
			if strings.Contains(string(content), "type "+name+" struct") {
				t.Errorf("retired handwritten HTTP contract %s returned in %s", name, path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
