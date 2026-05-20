package cleanup

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func TestPrintCleanupResultBranches(t *testing.T) {
	oldDryRun := cleanupDryRun
	t.Cleanup(func() { cleanupDryRun = oldDryRun })

	out := captureCleanupStdout(t, func() {
		if failed := printCleanupResult("Sessions", 2, 1, nil); failed {
			t.Fatal("successful sessions result reported failure")
		}
	})
	if !strings.Contains(out, "Sessions: purged 2, compacted 1 index entries") {
		t.Fatalf("sessions output = %q", out)
	}

	cleanupDryRun = true
	out = captureCleanupStdout(t, func() {
		if failed := printCleanupResult("Events", 3, 0, nil); failed {
			t.Fatal("successful events result reported failure")
		}
	})
	if !strings.Contains(out, "Events: would purge 3 files") {
		t.Fatalf("events dry-run output = %q", out)
	}

	out = captureCleanupStdout(t, func() {
		if failed := printCleanupResult("Usage", 0, 0, errors.New("boom")); !failed {
			t.Fatal("error result did not report failure")
		}
	})
	if !strings.Contains(out, "Usage: error: boom") {
		t.Fatalf("error output = %q", out)
	}
}

func TestParseDayDurationBranches(t *testing.T) {
	got, err := parseDayDuration("2d")
	if err != nil || got != 48*time.Hour {
		t.Fatalf("parseDayDuration(2d) = %v, %v", got, err)
	}
	got, err = parseDayDuration("90m")
	if err != nil || got != 90*time.Minute {
		t.Fatalf("parseDayDuration(90m) = %v, %v", got, err)
	}
	if _, err := parseDayDuration("xd"); err == nil {
		t.Fatal("parseDayDuration(xd) error = nil")
	}
}

func TestRunCleanupValidationBranchesAndDryRunSuccess(t *testing.T) {
	oldSessionsAge, oldUsageAge, oldEventsAge, oldDryRun := cleanupSessionsAge, cleanupUsageAge, cleanupEventsAge, cleanupDryRun
	t.Cleanup(func() {
		cleanupSessionsAge, cleanupUsageAge, cleanupEventsAge, cleanupDryRun = oldSessionsAge, oldUsageAge, oldEventsAge, oldDryRun
		cli.ResetWorkspaceRuntimeDirCache()
	})

	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	cli.ResetWorkspaceRuntimeDirCache()

	cleanupSessionsAge = "1d"
	cleanupUsageAge = "bad"
	cleanupEventsAge = "1d"
	cleanupDryRun = true
	if err := runCleanup(nil, nil); err == nil || !strings.Contains(err.Error(), "invalid --usage-older-than") {
		t.Fatalf("runCleanup invalid usage age err = %v", err)
	}

	cleanupUsageAge = "1d"
	cleanupEventsAge = "bad"
	if err := runCleanup(nil, nil); err == nil || !strings.Contains(err.Error(), "invalid --events-older-than") {
		t.Fatalf("runCleanup invalid events age err = %v", err)
	}

	cleanupEventsAge = "1d"
	out := captureCleanupStdout(t, func() {
		if err := runCleanup(nil, nil); err != nil {
			t.Fatalf("runCleanup dry-run: %v", err)
		}
	})
	for _, want := range []string{
		"Sessions: would purge 0, would compact 0 index entries",
		"Usage: would purge 0 records",
		"Events: would purge 0 files",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("runCleanup output = %q, missing %q", out, want)
		}
	}
}
