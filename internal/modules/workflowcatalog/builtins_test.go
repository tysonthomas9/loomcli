package workflowcatalog

import (
	"slices"
	"testing"
)

func TestBuiltinWorkflowRegistryIsCanonicalAndDefensive(t *testing.T) {
	want := []string{
		BuiltinBugFixAgentWorkflowName,
		BuiltinEpicRunnerWorkflowName,
		BuiltinGitHubReviewAgentWorkflowName,
		BuiltinLocalReviewAgentWorkflowName,
		BuiltinPromptAgentWorkflowName,
		BuiltinReviewLoopAgentWorkflowName,
	}
	got := BuiltinWorkflowNames()
	if !slices.Equal(got, want) || !slices.IsSorted(got) {
		t.Fatalf("BuiltinWorkflowNames = %v, want sorted %v", got, want)
	}
	got[0] = "tampered"
	if next := BuiltinWorkflowNames(); !slices.Equal(next, want) {
		t.Fatalf("BuiltinWorkflowNames aliases registry: %v", next)
	}
	for _, name := range want {
		if !IsBuiltinWorkflowName(name) {
			t.Fatalf("IsBuiltinWorkflowName(%q) = false", name)
		}
	}
	if IsBuiltinWorkflowName(BuiltinGitHubReviewTaskRunnerName) || IsBuiltinWorkflowName(" epic-runner ") {
		t.Fatal("task-runner or noncanonical identity admitted as a managed builtin")
	}
}

func TestBuiltinAuthoringReferencesAreContentDerived(t *testing.T) {
	sourceDigest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bundleDigest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	versionID := BuiltinVersionID(BuiltinEpicRunnerWorkflowName, bundleDigest)
	if versionID != "epic-runner-v-bbbbbbbbbbbb" {
		t.Fatalf("BuiltinVersionID = %q", versionID)
	}
	if got := BuiltinSourceRef(BuiltinEpicRunnerWorkflowName, sourceDigest); got != "builtin://workflows/epic-runner/versions/"+sourceDigest {
		t.Fatalf("BuiltinSourceRef = %q", got)
	}
	if got := BuiltinBundleRef(BuiltinEpicRunnerWorkflowName, versionID); got != ".loom/drivers/epic-runner/"+versionID {
		t.Fatalf("BuiltinBundleRef = %q", got)
	}
}
