package archtest

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBaselineValidationRequiresPassingCurrentHardGate(t *testing.T) {
	load := func(t *testing.T) Baseline {
		t.Helper()
		baseline, err := LoadBaseline(filepath.Join("testdata", "migration-baseline.json"))
		if err != nil {
			t.Fatal(err)
		}
		return baseline
	}

	t.Run("required run cannot be absent", func(t *testing.T) {
		baseline := load(t)
		runs := baseline.Validation.Snapshots[0].Runs
		baseline.Validation.Snapshots[0].Runs = runs[1:]
		if err := baseline.Validate(); err == nil || !strings.Contains(err.Error(), "missing required hard-gate run") {
			t.Fatalf("Validate error = %v, want missing hard-gate rejection", err)
		}
	})

	t.Run("required run cannot be unrecorded", func(t *testing.T) {
		baseline := load(t)
		baseline.Validation.Snapshots[0].Runs[0].Result.Status = "not-recorded"
		if err := baseline.Validate(); err == nil || !strings.Contains(err.Error(), "want pass") {
			t.Fatalf("Validate error = %v, want non-passing hard-gate rejection", err)
		}
	})

	t.Run("evidence arrays must be explicit", func(t *testing.T) {
		baseline := load(t)
		baseline.Validation.Snapshots[0].Runs[0].ArtifactPaths = nil
		if err := baseline.Validate(); err == nil || !strings.Contains(err.Error(), "explicitly record") {
			t.Fatalf("Validate error = %v, want omitted evidence rejection", err)
		}
	})

	t.Run("snapshot must match baseline source", func(t *testing.T) {
		baseline := load(t)
		baseline.Validation.Snapshots[0].LoomHead = "0123456789abcdef0123456789abcdef01234567"
		if err := baseline.Validate(); err == nil || !strings.Contains(err.Error(), "matching the current") {
			t.Fatalf("Validate error = %v, want source mismatch rejection", err)
		}
	})
}
