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
	for _, want := range []string{"mutation commands = 104, want 105", "module roots = [artifacts execution]"} {
		if !slices.ContainsFunc(violations, func(violation string) bool { return strings.Contains(violation, want) }) {
			t.Fatalf("snapshot violations = %v, want entry containing %q", violations, want)
		}
	}
}

func checkedInSnapshotReport() Report {
	return Report{
		CompositeStoreFiles:        make([]string, 66),
		CompositeStoreOutside:      make([]string, 56),
		LegacyHandlerImports:       make([]LegacyImportUse, 80),
		ModuleRoots:                append([]string(nil), checkedInModuleRoots...),
		AnalysisProfilesEnforced:   11,
		MutationCommands:           105,
		DirectPersistenceWrites:    224,
		RuntimeComponents:          71,
		RuntimeGoroutineLaunches:   80,
		PerformanceMetrics:         6,
		PerformanceMetricsMeasured: 6,
	}
}
