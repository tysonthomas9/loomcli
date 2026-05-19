package cleanup

import (
	"errors"
	"strings"
	"testing"
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
