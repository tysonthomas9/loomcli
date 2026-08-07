package authoring

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

func TestGitHubReviewAgentRegistersThroughTrustedBuiltinPath(t *testing.T) {
	if _, ok := BuiltinWorkflow(BuiltinGitHubReviewAgentWorkflowName); !ok {
		t.Fatal("EnsureBuiltinWorkflow would return ErrNotFound: github-review-agent not in the builtin registry")
	}
	if got := submissionTrust(workflowcatalog.DriverTrustTrusted); got != workflowcatalog.DriverTrustTrusted {
		t.Fatalf("submissionTrust(trusted) = %q, want trusted (builtin path)", got)
	}
	if got := submissionTrust(""); got != workflowcatalog.DriverTrustUntrusted {
		t.Fatalf("submissionTrust(\"\") = %q, want untrusted (external submissions fail closed)", got)
	}
}

func TestMigratedWorkflowAuthoringCallersCannotRegressToGenericPersistence(t *testing.T) {
	for _, rel := range []string{
		"../../../cli/serve/serveadapter/catalogcomposition/workflow_catalog.go",
		"../../../cli/workflow/workflow_cmd.go",
		"../../../webui/handlers/workflows/module.go",
		"builtin_authoring.go",
	} {
		content, err := os.ReadFile(filepath.Clean(rel))
		if err != nil {
			t.Fatalf("read migrated authoring caller %s: %v", rel, err)
		}
		source := string(content)
		for _, forbidden := range []string{
			"BuildAndRegister(",
			"RegisterFlueDriver(",
			"Drivers().Create(",
			"Drivers().Update(",
			"DriverVersions().Create(",
			"DriverVersions().Update(",
			"EnsureAndResolveDriver(",
		} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s contains forbidden legacy authoring call %q", rel, forbidden)
			}
		}
	}
}

func TestNativeDriverRegisterUsesTheManagementBoundary(t *testing.T) {
	content, err := os.ReadFile(filepath.Clean("../../../cli/driver/driver_cmd.go"))
	if err != nil {
		t.Fatalf("read native driver command: %v", err)
	}
	source := string(content)
	if strings.Contains(source, "driverpkg.RegisterFlueDriver(") {
		t.Fatal("native driver CLI bypasses the management API with a direct generic-store writer")
	}
	if !strings.Contains(source, "RegisterNativeDriver(") {
		t.Fatal("native driver CLI does not call the Workflow Catalog management boundary")
	}
}

func TestWorkflowRunnerSpecsDoNotInferCustomSiblingRunnerFiles(t *testing.T) {
	runners := workflowRunnerSpecs(BuildAndRegisterOptions{
		Entrypoint: "workflows/custom.ts",
		Files: map[string]string{
			"workflows/custom.ts":              "export async function run() {}",
			"workflows/local-task-runner.ts":   "export async function run() {}",
			"workflows/daytona-task-runner.ts": "export async function run() {}",
		},
	})
	if len(runners) != 0 {
		t.Fatalf("runner specs = %+v, want no inferred custom runners without explicit manifest", runners)
	}
}
