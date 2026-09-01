package cireport_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestObserverWorkflowUsesTrustedCodeAndLeastPrivilege(t *testing.T) {
	reusable := readWorkflowFile(t, ".github/workflows/skills-run-observer.yml")
	for _, required := range []string{
		"workflow_call:", "actions: read", "checks: write", "contents: read",
		"repository: tysonthomas9/loomcli", "ref: ${{ inputs.observer_ref }}", "persist-credentials: false",
		"go run ./cmd/skills-run-observer", "GITHUB_TOKEN: ${{ github.token }}",
	} {
		if !strings.Contains(reusable, required) {
			t.Errorf("reusable observer is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"contents: write", "pull-requests: write", "issues: write", "packages: write",
		"download-artifact", "github.event.workflow_run.head_repository",
	} {
		if strings.Contains(reusable, forbidden) {
			t.Errorf("reusable observer contains unsafe capability %q", forbidden)
		}
	}

	caller := readWorkflowFile(t, ".github/workflows/skills-compatibility-observer.yml")
	for _, required := range []string{
		"workflow_run:", `workflows: ["Skills compatibility"]`, "types: [completed]",
		"uses: ./.github/workflows/skills-run-observer.yml",
		"run_id: ${{ github.event.workflow_run.id }}",
		"head_sha: ${{ github.event.workflow_run.head_sha }}",
		"observer_ref: ${{ github.workflow_sha }}",
	} {
		if !strings.Contains(caller, required) {
			t.Errorf("observer caller is missing %q", required)
		}
	}
}

func readWorkflowFile(t *testing.T, name string) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	raw, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
