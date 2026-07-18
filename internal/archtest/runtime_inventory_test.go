package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckedInRuntimeInventoryMatchesRepository(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := LoadRuntimeInventory(filepath.Join("testdata", "runtime-components.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := CompareRuntimeTickerInventory(root, inventory); err != nil {
		t.Fatal(err)
	}
	if got, want := len(inventory.Components), 86; got != want {
		t.Fatalf("runtime components = %d, want baseline %d", got, want)
	}
	if got, want := len(inventory.GoroutineLaunches), 103; got != want {
		t.Fatalf("runtime goroutine launches = %d, want baseline %d", got, want)
	}
	codexLaunches := map[string]struct{}{
		"internal/leadcontrol/codex_runtime.go::RunCodexLeadRuntime::go#1": {},
		"internal/leadcontrol/codex_runtime.go::RunCodexLeadRuntime::go#2": {},
		"internal/leadcontrol/codex_runtime.go::startCodexAppServer::go#1": {},
	}
	for _, launch := range inventory.GoroutineLaunches {
		delete(codexLaunches, runtimeIdentity(launch.File, launch.Function, launch.Site))
	}
	if len(codexLaunches) != 0 {
		t.Fatalf("runtime goroutine inventory is missing Codex launch sites: %v", codexLaunches)
	}
	tickers := 0
	for _, component := range inventory.Components {
		if component.Kind == "ticker" {
			tickers++
		}
	}
	if got, want := tickers, 53; got != want {
		t.Fatalf("runtime ticker components = %d, want baseline %d", got, want)
	}
}

func TestRuntimeInventoryRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-components.yaml")
	contents := validRuntimeInventoryYAML() + "unknown: true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRuntimeInventory(path); err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("LoadRuntimeInventory error = %v, want strict unknown-field rejection", err)
	}
}

func TestRuntimeInventoryRejectsDuplicateIdentity(t *testing.T) {
	inventory := validRuntimeInventory()
	duplicate := inventory.Components[0]
	duplicate.ID = "duplicate"
	inventory.Components = append(inventory.Components, duplicate)
	if err := inventory.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate site") {
		t.Fatalf("Validate error = %v, want duplicate-site rejection", err)
	}
}

func TestRuntimeInventoryRejectsDuplicateID(t *testing.T) {
	inventory := validRuntimeInventory()
	duplicate := inventory.Components[0]
	duplicate.Site = "time.NewTicker#2"
	inventory.Components = append(inventory.Components, duplicate)
	if err := inventory.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate component id") {
		t.Fatalf("Validate error = %v, want duplicate-id rejection", err)
	}
}

func TestRuntimeInventoryRequiresLifecycleFields(t *testing.T) {
	inventory := validRuntimeInventory()
	inventory.Components[0].ReadinessHealth = ""
	if err := inventory.Validate(); err == nil || !strings.Contains(err.Error(), "readiness_health must not be empty") {
		t.Fatalf("Validate error = %v, want missing lifecycle-field rejection", err)
	}
}

func TestRuntimeInventoryRequiresGoroutineLaunchLedger(t *testing.T) {
	inventory := validRuntimeInventory()
	inventory.GoroutineLaunches = nil
	if err := inventory.Validate(); err == nil || !strings.Contains(err.Error(), "goroutine_launches must not be empty") {
		t.Fatalf("Validate error = %v, want empty goroutine-launch ledger rejection", err)
	}
}

func TestRuntimeInventoryRequiresDefaultDenyGoroutineDispositionPolicy(t *testing.T) {
	inventory := validRuntimeInventory()
	inventory.GoroutineDispositionPolicy = "review-optional"
	if err := inventory.Validate(); err == nil || !strings.Contains(err.Error(), "goroutine_disposition_policy") {
		t.Fatalf("Validate error = %v, want default-deny disposition-policy rejection", err)
	}
}

func TestRuntimeInventoryRequiresReviewedGoroutineDisposition(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RuntimeGoroutineLaunch)
		want   string
	}{
		{
			name: "missing disposition",
			mutate: func(launch *RuntimeGoroutineLaunch) {
				launch.Disposition = ""
			},
			want: "disposition",
		},
		{
			name: "invalid disposition",
			mutate: func(launch *RuntimeGoroutineLaunch) {
				launch.Disposition = "unreviewed"
			},
			want: "must be one of",
		},
		{
			name: "missing component link",
			mutate: func(launch *RuntimeGoroutineLaunch) {
				launch.ComponentIDs = nil
			},
			want: "requires at least one component_id",
		},
		{
			name: "stale component link",
			mutate: func(launch *RuntimeGoroutineLaunch) {
				launch.ComponentIDs = []string{"missing-component"}
			},
			want: "does not resolve to a lifecycle component",
		},
		{
			name: "invalid component link",
			mutate: func(launch *RuntimeGoroutineLaunch) {
				launch.ComponentIDs = []string{"Not Kebab Case"}
			},
			want: "must be lowercase kebab-case",
		},
		{
			name: "unsorted component links",
			mutate: func(launch *RuntimeGoroutineLaunch) {
				launch.ComponentIDs = []string{"example-ticker", "another-component"}
			},
			want: "must be sorted",
		},
		{
			name: "duplicate component links",
			mutate: func(launch *RuntimeGoroutineLaunch) {
				launch.ComponentIDs = []string{"example-ticker", "example-ticker"}
			},
			want: "duplicate runtime goroutine component link",
		},
		{
			name: "component link with exemption reason",
			mutate: func(launch *RuntimeGoroutineLaunch) {
				launch.Reason = "not actually linked"
			},
			want: "must not include an exemption reason",
		},
		{
			name: "helper without reason",
			mutate: func(launch *RuntimeGoroutineLaunch) {
				launch.Disposition = "helper"
				launch.ComponentIDs = nil
				launch.Reason = ""
			},
			want: "requires a non-empty single-line reason",
		},
		{
			name: "helper with stale component link",
			mutate: func(launch *RuntimeGoroutineLaunch) {
				launch.Disposition = "helper"
				launch.ComponentIDs = []string{"example-ticker"}
				launch.Reason = "bounded helper"
			},
			want: "must not include component_ids",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inventory := validRuntimeInventory()
			test.mutate(&inventory.GoroutineLaunches[0])
			if err := inventory.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRuntimeInventoryRejectsMalformedUnsortedAndDuplicateGoroutineLaunches(t *testing.T) {
	tests := []struct {
		name     string
		launches []RuntimeGoroutineLaunch
		want     string
	}{
		{
			name:     "malformed site",
			launches: []RuntimeGoroutineLaunch{{File: "internal/example/example.go", Function: "Run", Site: "goroutine#1", Callee: "work", Disposition: "helper", Reason: "bounded test helper"}},
			want:     "must use go#N",
		},
		{
			name:     "missing callee",
			launches: []RuntimeGoroutineLaunch{{File: "internal/example/example.go", Function: "Run", Site: "go#1", Disposition: "helper", Reason: "bounded test helper"}},
			want:     "callee must be a non-empty single-line value",
		},
		{
			name: "unsorted",
			launches: []RuntimeGoroutineLaunch{
				{File: "internal/z/z.go", Function: "Run", Site: "go#1", Callee: "work", Disposition: "helper", Reason: "bounded test helper"},
				{File: "internal/a/a.go", Function: "Run", Site: "go#1", Callee: "work", Disposition: "helper", Reason: "bounded test helper"},
			},
			want: "must be sorted",
		},
		{
			name: "duplicate",
			launches: []RuntimeGoroutineLaunch{
				{File: "internal/example/example.go", Function: "Run", Site: "go#1", Callee: "work", Disposition: "helper", Reason: "bounded test helper"},
				{File: "internal/example/example.go", Function: "Run", Site: "go#1", Callee: "work", Disposition: "helper", Reason: "bounded test helper"},
			},
			want: "duplicate runtime goroutine launch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inventory := validRuntimeInventory()
			inventory.GoroutineLaunches = test.launches
			if err := inventory.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRuntimeInventoryScannerExcludesTestsGeneratedAndNamedDirectories(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/live/live.go", `package live
import "time"
func Run() { _ = time.NewTicker(time.Second); go func() {}() }
`)
	writeGoFile(t, root, "internal/live/live_test.go", `package live
import "time"
func TestRun() { _ = time.NewTicker(time.Second); go func() {}() }
`)
	writeGoFile(t, root, "internal/generated/generated.go", `// Code generated by test. DO NOT EDIT.
package generated
import "time"
func Run() { _ = time.NewTicker(time.Second); go func() {}() }
`)
	writeGoFile(t, root, "internal/vendor/hidden.go", `package vendor
import "time"
func Run() { _ = time.NewTicker(time.Second); go func() {}() }
`)

	inventory := validRuntimeInventory()
	sites, err := ScanProductionTickerSites(root, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 || sites[0].File != "internal/live/live.go" || sites[0].Function != "Run" {
		t.Fatalf("ticker sites = %+v, want only production live.Run", sites)
	}
	launches, err := ScanProductionGoroutineSites(root, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if len(launches) != 1 || launches[0].File != "internal/live/live.go" || launches[0].Function != "Run" || launches[0].Site != "go#1" || launches[0].Callee != "<anonymous>" {
		t.Fatalf("goroutine launches = %+v, want only production live.Run go#1", launches)
	}
}

func TestRuntimeInventoryScannerUsesStableMethodAndOrdinalIdentity(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/poller/poller.go", `package poller
import tm "time"
type Poller struct{}
func (p *Poller) Run() {
	_ = tm.NewTicker(tm.Second)
	_ = tm.NewTicker(2 * tm.Second)
}
`)
	sites, err := ScanProductionTickerSites(root, validRuntimeInventory())
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 || sites[0].Function != "Poller.Run" || sites[0].Site != "time.NewTicker#1" || sites[1].Site != "time.NewTicker#2" {
		t.Fatalf("ticker sites = %+v, want stable receiver and ordinal identities", sites)
	}
}
func TestRuntimeInventoryScannerUsesOneOrdinalSequenceForPackageInitializers(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/poller/poller.go", `package poller
import "time"
var first = time.NewTicker(time.Second)
var second = time.NewTicker(2 * time.Second)
`)
	sites, err := ScanProductionTickerSites(root, validRuntimeInventory())
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 || sites[0].Function != "<package>" || sites[0].Site != "time.NewTicker#1" || sites[1].Site != "time.NewTicker#2" {
		t.Fatalf("ticker sites = %+v, want one package-level ordinal sequence", sites)
	}
}

func TestRuntimeGoroutineScannerUsesStableMethodOrdinalAndNestedIdentity(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/poller/poller.go", `package poller
type Poller struct{}
func (p *Poller) work() {}
func (p *Poller) Run() {
	go p.work()
	go func() {
		go p.work()
	}()
}
`)
	launches, err := ScanProductionGoroutineSites(root, validRuntimeInventory())
	if err != nil {
		t.Fatal(err)
	}
	if len(launches) != 3 {
		t.Fatalf("goroutine launches = %+v, want three sites", launches)
	}
	wantSites := []string{"go#1", "go#2", "go#3"}
	for index, launch := range launches {
		wantSite := wantSites[index]
		wantCallee := "p.work"
		if index == 1 {
			wantCallee = "<anonymous>"
		}
		if launch.Function != "Poller.Run" || launch.Site != wantSite || launch.Callee != wantCallee {
			t.Fatalf("goroutine launch[%d] = %+v, want Poller.Run %s", index, launch, wantSite)
		}
	}
}

func TestRuntimeGoroutineScannerUsesPackageIdentityForInitializerClosures(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/worker/worker.go", `package worker
func work() {}
var started = func() bool {
	go work()
	return true
}()
`)
	launches, err := ScanProductionGoroutineSites(root, validRuntimeInventory())
	if err != nil {
		t.Fatal(err)
	}
	if len(launches) != 1 || launches[0].Function != "<package>" || launches[0].Site != "go#1" || launches[0].Callee != "work" {
		t.Fatalf("goroutine launches = %+v, want package-level go#1", launches)
	}
}

func TestRuntimeInventoryComparisonRejectsAdditionsAndRemovals(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/live/live.go", `package live
import "time"
func Run() { _ = time.NewTicker(time.Second) }
`)
	inventory := validRuntimeInventory()
	if err := CompareRuntimeTickerInventory(root, inventory); err == nil || !strings.Contains(err.Error(), "missing ticker") || !strings.Contains(err.Error(), "stale ticker") {
		t.Fatalf("CompareRuntimeTickerInventory error = %v, want addition and removal failures", err)
	}
}

func TestRuntimeInventoryComparisonRejectsGoroutineAdditionsAndRemovals(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/live/live.go", `package live
func Run() { go func() {}() }
`)
	inventory := validRuntimeInventory()
	err := CompareRuntimeTickerInventory(root, inventory)
	if err == nil || !strings.Contains(err.Error(), "missing goroutine launch internal/live/live.go::Run::go#1") ||
		!strings.Contains(err.Error(), "stale goroutine launch internal/example/example.go::Run::go#1") {
		t.Fatalf("CompareRuntimeTickerInventory error = %v, want goroutine addition and removal failures", err)
	}
}

func TestRuntimeInventoryComparisonRejectsGoroutineCalleeChange(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/example/example.go", `package example
import "time"
func other() {}
func Run() {
	_ = time.NewTicker(time.Second)
	go other()
}
`)
	inventory := validRuntimeInventory()
	err := CompareRuntimeTickerInventory(root, inventory)
	if err == nil || !strings.Contains(err.Error(), `goroutine launch internal/example/example.go::Run::go#1 callee changed from "work" to "other"`) {
		t.Fatalf("CompareRuntimeTickerInventory error = %v, want callee-change rejection", err)
	}
}

func TestRuntimeInventoryComparisonRejectsStaleComponentReference(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/example/example.go", `package example
func Run() {}
`)
	inventory := validRuntimeInventory()
	inventory.Components[0] = RuntimeComponent{
		ID:              "example-component",
		Kind:            "component",
		File:            "internal/example/example.go",
		Function:        "Missing",
		Site:            "component:loop",
		Capability:      "platform",
		Owner:           "platform runtime",
		Cadence:         "event driven",
		Cancellation:    "context cancellation",
		ReadinessHealth: "not readiness-bearing",
		RetryBackoff:    "none",
		Classification:  "managed",
	}
	inventory.GoroutineLaunches[0].ComponentIDs = []string{"example-component"}
	if err := CompareRuntimeTickerInventory(root, inventory); err == nil || !strings.Contains(err.Error(), "stale component") || !strings.Contains(err.Error(), "function does not exist") {
		t.Fatalf("CompareRuntimeTickerInventory error = %v, want stale component function failure", err)
	}
}

func TestRuntimeInventoryRejectsTestFileComponent(t *testing.T) {
	inventory := validRuntimeInventory()
	inventory.Components[0].File = "internal/example/example_test.go"
	if err := inventory.Validate(); err == nil || !strings.Contains(err.Error(), "must be production source") {
		t.Fatalf("Validate error = %v, want test-file rejection", err)
	}
}

func validRuntimeInventory() RuntimeInventory {
	return RuntimeInventory{
		SchemaVersion:              runtimeInventorySchemaVersion,
		ProductionRoots:            append([]string(nil), productionRoots...),
		GoroutineDispositionPolicy: "default-deny",
		Exclusions: RuntimeExclusions{
			TestFiles:      true,
			GeneratedFiles: true,
			Directories:    append([]string(nil), excludedDirNames...),
		},
		GoroutineLaunches: []RuntimeGoroutineLaunch{{
			File: "internal/example/example.go", Function: "Run", Site: "go#1", Callee: "work", Disposition: "component", ComponentIDs: []string{"example-ticker"},
		}},
		Components: []RuntimeComponent{{
			ID:              "example-ticker",
			Kind:            "ticker",
			File:            "internal/example/example.go",
			Function:        "Run",
			Site:            "time.NewTicker#1",
			Capability:      "platform",
			Owner:           "platform runtime",
			Cadence:         "1s",
			Cancellation:    "context cancellation",
			ReadinessHealth: "not readiness-bearing",
			RetryBackoff:    "fixed cadence; no retry loop",
			Classification:  "managed",
		}},
	}
}

func validRuntimeInventoryYAML() string {
	return `schema_version: 2
production_roots: [cmd, internal, sdk]
exclusions:
  test_files: true
  generated_files: true
  directories: [.git, node_modules, third_party, vendor, worktrees]
goroutine_disposition_policy: default-deny
goroutine_launches:
  - file: internal/example/example.go
    function: Run
    site: go#1
    callee: work
    disposition: component
    component_ids: [example-ticker]
components:
  - id: example-ticker
    kind: ticker
    file: internal/example/example.go
    function: Run
    site: time.NewTicker#1
    capability: platform
    owner: platform runtime
    cadence: 1s
    cancellation: context cancellation
    readiness_health: not readiness-bearing
    retry_backoff: fixed cadence; no retry loop
    classification: managed
`
}
