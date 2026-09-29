package tsruntime

import (
	"strings"
	"testing"
)

func TestLeafPatchRequiresTaskAndRepoIdentityBeforeApplying(t *testing.T) {
	for _, tc := range []struct {
		name, patch, task, repo, missing string
	}{
		{"no patch", "", "", "", ""},
		{"missing task", "patch", "", "source-repo", "LOOM_ASSIGNED_TASK_ID"},
		{"missing repo", "patch", "TASK-1", "", "LOOM_AGENT_REPO"},
		{"both present", "patch", "TASK-1", "source-repo", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLeafPatchIdentity(tc.patch, tc.task, tc.repo)
			if tc.missing == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.missing != "" && (err == nil || !strings.Contains(err.Error(), tc.missing)) {
				t.Fatalf("error = %v, want %s", err, tc.missing)
			}
		})
	}
}
