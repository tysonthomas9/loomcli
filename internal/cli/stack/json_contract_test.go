package stack

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

func TestStackJSONContractIsMappedAtCLIBoundary(t *testing.T) {
	encoded, err := json.Marshal(stackForJSON(sourcecontrol.Stack{
		ID: "epic:E", WorkspaceKey: "WS", Repository: "repo", RootBase: "main",
		DefaultCommitMode: sourcecontrol.CommitModeLoom,
		CreatedAt:         time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(),
	}))
	if err != nil {
		t.Fatalf("marshal CLI stack contract: %v", err)
	}
	value := string(encoded)
	for _, key := range []string{`"id"`, `"workspaceKey"`, `"repoName"`, `"rootBase"`, `"defaultCommitMode"`} {
		if !strings.Contains(value, key) {
			t.Errorf("CLI stack JSON %s does not contain %s", value, key)
		}
	}
	if strings.Contains(value, `"WorkspaceKey"`) || strings.Contains(value, `"Repository"`) {
		t.Fatalf("CLI stack JSON leaked owner field names: %s", value)
	}
}

func TestStackNodeJSONContractIsMappedAtCLIBoundary(t *testing.T) {
	encoded, err := json.Marshal(stackNodeForJSON(sourcecontrol.StackNode{
		StackID: "epic:E", TaskID: "E-1", OutputBranch: "stack/e/e-1",
		State: sourcecontrol.NodeStatePending,
	}))
	if err != nil {
		t.Fatalf("marshal CLI stack-node contract: %v", err)
	}
	value := string(encoded)
	for _, key := range []string{`"stackId"`, `"taskId"`, `"outputBranch"`, `"state"`} {
		if !strings.Contains(value, key) {
			t.Errorf("CLI stack-node JSON %s does not contain %s", value, key)
		}
	}
}
