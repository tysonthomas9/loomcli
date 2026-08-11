package workflows

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestEnsureBuiltinWorkflowRegistersBundledArtifactWithoutBuildToolchain(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runners []string
	}{
		{name: BuiltinEpicRunnerWorkflowName, runners: []string{"local-task-runner", "daytona-task-runner"}},
		{name: BuiltinGitHubReviewAgentWorkflowName, runners: []string{BuiltinGitHubReviewTaskRunnerName}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := memstore.New()
			if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BUNDLED", Name: "Bundled"}); err != nil {
				t.Fatalf("create workspace: %v", err)
			}

			resourceRoot := t.TempDir()
			dist := filepath.Join(resourceRoot, tc.name)
			if err := os.MkdirAll(dist, 0o755); err != nil {
				t.Fatalf("create bundled dist: %v", err)
			}
			server := `if (process.send) {
  process.send({ version: 1, type: 'ready', target: 'workflow', name: process.env.FLUE_CLI_NAME || 'epic-runner' });
}
`
			if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte(server), 0o644); err != nil {
				t.Fatalf("write bundled server: %v", err)
			}

			t.Setenv("LOOM_BUILTIN_WORKFLOW_BUNDLES_DIR", resourceRoot)
			t.Setenv("LOOM_SDK_ROOT", "")
			t.Setenv("LOOM_FLUE_RUNTIME_ROOT", "")
			t.Setenv("FLUE_RUNTIME_ROOT", "")
			t.Setenv("FLUE_REPO", "")
			t.Setenv("LOOM_REAL_FLUE_CMD_JSON", "")
			t.Setenv("LOOM_REAL_FLUE_CMD", "")
			t.Chdir(t.TempDir())

			if err := EnsureBuiltinWorkflow(ctx, st, "BUNDLED", tc.name); err != nil {
				t.Fatalf("EnsureBuiltinWorkflow from packaged artifact: %v", err)
			}
			driverRecord, err := st.Drivers().Get(ctx, "BUNDLED", tc.name)
			if err != nil {
				t.Fatalf("get bundled driver: %v", err)
			}
			if driverRecord.Status != domain.DriverStatusActive || driverRecord.ActiveVersionID == "" {
				t.Fatalf("driver = %+v, want active bundled version", driverRecord)
			}
			version, err := st.DriverVersions().Get(ctx, "BUNDLED", driverRecord.ActiveVersionID)
			if err != nil {
				t.Fatalf("get bundled version: %v", err)
			}
			if got := version.Manifest["trust_level"]; got != string(domain.DriverTrustTrusted) {
				t.Fatalf("trust_level = %q, want trusted", got)
			}
			for _, runner := range tc.runners {
				if !strings.Contains(version.Manifest["runners"], runner) {
					t.Fatalf("runners manifest %q missing %q", version.Manifest["runners"], runner)
				}
			}
		})
	}
}
