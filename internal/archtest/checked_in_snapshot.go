package archtest

import (
	"fmt"
	"slices"
)

var checkedInModuleRoots = []string{"artifacts", "automation", "execution", "workflowcatalog"}

// checkedInSnapshotViolations keeps the current migration snapshot exact while
// the ordinary ratchets prevent new coupling. Keeping this validation in the
// production repository check lets the aggregate gate run one full analysis
// without relying on a second repository-scale test invocation.
func checkedInSnapshotViolations(report Report) []string {
	violations := make([]string, 0)
	checkCount := func(label string, got, want int) {
		if got != want {
			violations = append(violations, fmt.Sprintf("checked-in architecture snapshot %s = %d, want %d", label, got, want))
		}
	}

	checkCount("composite Store files", len(report.CompositeStoreFiles), 82)
	checkCount("outside-composition Store files", len(report.CompositeStoreOutside), 71)
	checkCount("legacy handler imports", len(report.LegacyHandlerImports), 90)
	if !slices.Equal(report.ModuleRoots, checkedInModuleRoots) {
		violations = append(violations, fmt.Sprintf("checked-in architecture snapshot module roots = %v, want %v", report.ModuleRoots, checkedInModuleRoots))
	}
	checkCount("pending decisions", len(report.PendingDecisions), 0)
	checkCount("enforced analysis profiles", report.AnalysisProfilesEnforced, 11)
	checkCount("mutation commands", report.MutationCommands, 60)
	checkCount("runtime components", report.RuntimeComponents, 86)
	checkCount("runtime goroutine launches", report.RuntimeGoroutineLaunches, 103)
	checkCount("performance metrics", report.PerformanceMetrics, 6)
	checkCount("measured performance metrics", report.PerformanceMetricsMeasured, 6)
	checkCount("deferred performance metrics", report.PerformanceMetricsDeferred, 0)
	return violations
}
