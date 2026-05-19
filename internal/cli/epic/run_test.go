package epic

import (
	"context"
	"strings"
	"testing"
)

func TestEpicRunFlagValidationAndDerivedValues(t *testing.T) {
	resetEpicRunFlags(t)
	if err := validateEpicRunFlags(); err == nil || !strings.Contains(err.Error(), "--parent") {
		t.Fatalf("missing parent err = %v", err)
	}
	runParent = "EPIC-1"
	runMaxConcurrency = 0
	if err := validateEpicRunFlags(); err == nil || !strings.Contains(err.Error(), "--max-concurrency") {
		t.Fatalf("bad concurrency err = %v", err)
	}
	runMaxConcurrency = 2
	runIntervalSeconds = 0
	if err := validateEpicRunFlags(); err == nil || !strings.Contains(err.Error(), "--interval-seconds") {
		t.Fatalf("bad interval err = %v", err)
	}
	runIntervalSeconds = 5
	if err := validateEpicRunFlags(); err != nil {
		t.Fatalf("valid flags err = %v", err)
	}
	if got := epicRunWorkerPrefix(); got != "epic-1" {
		t.Fatalf("derived worker prefix = %q", got)
	}
	runWorkerPrefix = "custom"
	if got := epicRunWorkerPrefix(); got != "custom" {
		t.Fatalf("custom worker prefix = %q", got)
	}
}

func TestResolveLeadNameAndSignalContext(t *testing.T) {
	t.Setenv(envAgentName, "env-lead")
	if got := resolveLeadName(" flag-lead "); got != "flag-lead" {
		t.Fatalf("flag lead = %q", got)
	}
	if got := resolveLeadName(""); got != "env-lead" {
		t.Fatalf("env lead = %q", got)
	}
	ctx, cancel := signalContext(context.Background())
	cancel()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("signal context not cancelled after cancel")
	}
}

func resetEpicRunFlags(t *testing.T) {
	t.Helper()
	origParent, origPrefix, origRole, origNode, origLead := runParent, runWorkerPrefix, runRole, runNodeID, runLead
	origMax, origInterval := runMaxConcurrency, runIntervalSeconds
	origDry := runDryRun
	t.Cleanup(func() {
		runParent, runWorkerPrefix, runRole, runNodeID, runLead = origParent, origPrefix, origRole, origNode, origLead
		runMaxConcurrency, runIntervalSeconds = origMax, origInterval
		runDryRun = origDry
	})
	runParent = ""
	runWorkerPrefix = ""
	runRole = "task"
	runNodeID = ""
	runLead = ""
	runMaxConcurrency = 2
	runIntervalSeconds = 5
	runDryRun = false
}
