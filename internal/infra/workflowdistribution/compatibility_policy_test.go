package workflowdistribution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigratedWorkflowAuthoringCallersCannotRegressToGenericPersistence(t *testing.T) {
	for _, rel := range []string{
		"../../cli/serve/serveadapter/workflow_catalog.go",
		"../../cli/workflow/workflow_cmd.go",
		"../../webui/handlers/workflows/module.go",
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
	content, err := os.ReadFile(filepath.Clean("../../cli/driver/driver_cmd.go"))
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
