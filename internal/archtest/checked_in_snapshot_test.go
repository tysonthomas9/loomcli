package archtest

import (
	"slices"
	"strings"
	"testing"
)

func TestCheckedInSnapshotViolations(t *testing.T) {
	report := checkedInSnapshotReport()
	if violations := checkedInSnapshotViolations(report); len(violations) != 0 {
		t.Fatalf("matching report violations = %v", violations)
	}

	report.MutationCommands--
	report.ModuleRoots = []string{"artifacts", "execution"}
	violations := checkedInSnapshotViolations(report)
	for _, want := range []string{"mutation commands = 101, want 102", "module roots = [artifacts execution]"} {
		if !slices.ContainsFunc(violations, func(violation string) bool { return strings.Contains(violation, want) }) {
			t.Fatalf("snapshot violations = %v, want entry containing %q", violations, want)
		}
	}
}

func checkedInSnapshotReport() Report {
	return Report{
		CompositeStoreFiles:        make([]string, 78),
		CompositeStoreOutside:      make([]string, 66),
		LegacyHandlerImports:       make([]LegacyImportUse, 87),
		ModuleRoots:                append([]string(nil), checkedInModuleRoots...),
		AnalysisProfilesEnforced:   11,
		MutationCommands:           102,
		DirectPersistenceWrites:    256,
		RuntimeComponents:          90,
		RuntimeGoroutineLaunches:   105,
		PerformanceMetrics:         6,
		PerformanceMetricsMeasured: 6,
	}
}
