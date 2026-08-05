//go:build e2e

package daytonabroker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

// TestE2EDaytonaProviderBroker is intentionally opt-in: it provisions a real
// Daytona sandbox and may use a paid Codex backend. The test harness seals the
// supplied credential into a temporary Loom vault, clears the raw env value,
// and then exercises the same host broker composed by loom serve.
func TestE2EDaytonaProviderBroker(t *testing.T) {
	if os.Getenv("LOOM_DAYTONA_BROKER_E2E") != "1" {
		t.Skip("set LOOM_DAYTONA_BROKER_E2E=1 to run the paid external-service test")
	}
	daytonaKey := strings.TrimSpace(os.Getenv("DAYTONA_API_KEY"))
	if daytonaKey == "" {
		t.Fatal("DAYTONA_API_KEY is required by the test harness")
	}
	dataDir := t.TempDir()
	settings := localsettings.Default()
	sealed, err := localsettings.SealRuntimeCredential(
		dataDir,
		localsettings.RuntimeCredentialProviderDaytona,
		daytonaKey,
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	settings.RuntimeCredentials.Daytona = sealed
	if err := localsettings.Save(dataDir, settings); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DAYTONA_API_KEY", "")

	model := strings.TrimSpace(os.Getenv("LOOM_DAYTONA_E2E_MODEL"))
	result, err := NewDaytonaProviderBroker(dataDir).ExecuteDaytona(
		context.Background(),
		execution.DaytonaProviderCommand{
			WorkspaceKey: "DAYTONA-E2E",
			TaskRunID:    "daytona-provider-e2e",
			WorkItemID:   "DAYTONA-E2E-1",
			DriverRunID:  "daytona-provider-e2e-driver",
			Intent: execution.DaytonaProviderIntent{
				SchemaVersion: execution.DaytonaProviderSchemaV1,
				RepositoryURL: "https://github.com/octocat/Hello-World.git",
				TaskPrompt:    "Inspect the repository and summarize it. Do not change files.",
				Backend:       "codex",
				Model:         model,
				Delivery:      execution.DaytonaProviderDelivery{},
			},
		},
	)
	if err != nil {
		t.Fatalf("ExecuteDaytona: %v", err)
	}
	if result.Status != "completed" || result.Sandbox.Provider != "daytona" || result.Sandbox.ID == "" {
		t.Fatalf("unexpected Daytona result: %+v", result)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), daytonaKey) {
		t.Fatal("opaque provider receipt leaked the Daytona credential")
	}
}
