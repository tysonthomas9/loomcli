package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const checkedPerformanceBaseline = "testdata/performance-baseline.yaml"

func TestCheckedInPerformanceInventoryIsComplete(t *testing.T) {
	if _, err := LoadPerformanceInventory(checkedPerformanceBaseline); err != nil {
		t.Fatal(err)
	}
}

func TestPerformanceInventoryMatchesRuntimeComponentReference(t *testing.T) {
	performance, err := LoadPerformanceInventory(checkedPerformanceBaseline)
	if err != nil {
		t.Fatal(err)
	}
	runtimeInventory, err := LoadRuntimeInventory(filepath.Join("testdata", "runtime-components.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if err := ComparePerformanceRuntimeInventory(performance, runtimeInventory); err != nil {
		t.Fatal(err)
	}
}

func TestPerformanceInventoryRejectsRuntimeInventoryDrift(t *testing.T) {
	performance, err := LoadPerformanceInventory(checkedPerformanceBaseline)
	if err != nil {
		t.Fatal(err)
	}
	runtimeInventory, err := LoadRuntimeInventory(filepath.Join("testdata", "runtime-components.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	runtimeInventory.Components = runtimeInventory.Components[1:]
	if err := ComparePerformanceRuntimeInventory(performance, runtimeInventory); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("ComparePerformanceRuntimeInventory error = %v, want drift rejection", err)
	}
}

func TestPerformanceInventoryRejectsGoroutineLaunchInventoryDrift(t *testing.T) {
	performance, err := LoadPerformanceInventory(checkedPerformanceBaseline)
	if err != nil {
		t.Fatal(err)
	}
	runtimeInventory, err := LoadRuntimeInventory(filepath.Join("testdata", "runtime-components.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	runtimeInventory.GoroutineLaunches = runtimeInventory.GoroutineLaunches[1:]
	if err := ComparePerformanceRuntimeInventory(performance, runtimeInventory); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("ComparePerformanceRuntimeInventory error = %v, want goroutine-launch drift rejection", err)
	}
}

func TestPerformanceInventoryStrictlyRejectsUnknownField(t *testing.T) {
	contents := readPerformanceBaseline(t) + "\nunknown: true\n"
	_, err := LoadPerformanceInventory(writePerformanceBaseline(t, contents))
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("LoadPerformanceInventory error = %v, want strict unknown-field rejection", err)
	}
}

func TestPerformanceInventoryRequiresExplicitNullForUnavailableValue(t *testing.T) {
	contents := readPerformanceBaseline(t)
	contents = strings.Replace(contents, "      value: null\n      unit: milliseconds", "      unit: milliseconds", 1)
	_, err := LoadPerformanceInventory(writePerformanceBaseline(t, contents))
	if err == nil || !strings.Contains(err.Error(), "p50 must explicitly include value") {
		t.Fatalf("LoadPerformanceInventory error = %v, want missing-value rejection", err)
	}
}

func TestPerformanceInventoryRejectsIncompleteOrIncoherentRecords(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PerformanceInventory)
		want   string
	}{
		{
			name: "source SHA",
			mutate: func(inventory *PerformanceInventory) {
				inventory.LoomServeStartupReadiness.Record.Evidence.SourceSHA = "short"
			},
			want: "source_sha must be a 40-character",
		},
		{
			name: "measured at",
			mutate: func(inventory *PerformanceInventory) {
				inventory.LoomServeStartupReadiness.Record.Evidence.MeasuredAt = "yesterday"
			},
			want: "measured_at must be an ISO date",
		},
		{
			name: "evidence source",
			mutate: func(inventory *PerformanceInventory) {
				inventory.LoomServeStartupReadiness.Record.Evidence.EvidenceSource = ""
			},
			want: "requires evidence_source and procedure",
		},
		{
			name: "measured command",
			mutate: func(inventory *PerformanceInventory) {
				inventory.LoomServeStartupReadiness.Record.Evidence.Command = []string{}
			},
			want: "measured status requires a command",
		},
		{
			name: "dependencies field",
			mutate: func(inventory *PerformanceInventory) {
				inventory.LoomServeStartupReadiness.Record.Evidence.Environment.Dependencies = nil
			},
			want: "environment.dependencies must be explicitly recorded",
		},
		{
			name: "invented unavailable latency",
			mutate: func(inventory *PerformanceInventory) {
				inventory.WorkflowApprovalLatency.Result.P50.Value = RecordedNumber{Present: true, Valid: true, Value: 1}
			},
			want: "p50 must be null while status is not-yet-migrated",
		},
		{
			name: "unavailable command",
			mutate: func(inventory *PerformanceInventory) {
				inventory.FleetDBRoundTrips.Record.Evidence.Command = []string{"legacy-probe"}
			},
			want: "unavailable status requires command: []",
		},
		{
			name: "startup percentile",
			mutate: func(inventory *PerformanceInventory) {
				inventory.LoomServeStartupReadiness.Result.ReadinessP50.Value.Value++
			},
			want: "want nearest-rank",
		},
		{
			name: "background classifications",
			mutate: func(inventory *PerformanceInventory) {
				inventory.ProductionBackgroundLoops.Result.TotalComponents++
			},
			want: "does not equal total_components",
		},
		{
			name: "background goroutine launch sites",
			mutate: func(inventory *PerformanceInventory) {
				inventory.ProductionBackgroundLoops.Result.GoroutineLaunchSites = 0
			},
			want: "goroutine-launch",
		},
		{
			name: "route ordering",
			mutate: func(inventory *PerformanceInventory) {
				routes := inventory.FrontendRouteChunkSizes.Result.Routes
				routes[0], routes[1] = routes[1], routes[0]
			},
			want: "frontend routes must be sorted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inventory, err := LoadPerformanceInventory(checkedPerformanceBaseline)
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(&inventory)
			err = inventory.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate error = %v, want %q", err, tt.want)
			}
		})
	}
}

func readPerformanceBaseline(t *testing.T) string {
	t.Helper()
	contents, err := os.ReadFile(checkedPerformanceBaseline)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func writePerformanceBaseline(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "performance-baseline.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
