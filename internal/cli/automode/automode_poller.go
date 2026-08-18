package automode

import (
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
)

// useFixedPolling allows reverting to fixed 200ms polling via environment variable
var useFixedPolling = os.Getenv("LOOM_FIXED_POLLING") != ""

// adaptivePoller implements exponential backoff for polling intervals
type adaptivePoller struct {
	minInterval     time.Duration
	maxInterval     time.Duration
	currentInterval time.Duration
	backoffFactor   float64
}

// newAdaptivePoller creates a poller with sensible defaults
func newAdaptivePoller() *adaptivePoller {
	return &adaptivePoller{
		minInterval:     100 * time.Millisecond,  // Fast when active
		maxInterval:     1000 * time.Millisecond, // Slow when idle
		currentInterval: 200 * time.Millisecond,
		backoffFactor:   1.5,
	}
}

// tick returns a channel that fires after the current interval
func (p *adaptivePoller) tick() <-chan time.Time {
	return time.After(p.currentInterval)
}

// hadActivity resets to fast polling
func (p *adaptivePoller) hadActivity() {
	p.currentInterval = p.minInterval
}

// hadNoActivity increases the polling interval (exponential backoff)
func (p *adaptivePoller) hadNoActivity() {
	newInterval := time.Duration(float64(p.currentInterval) * p.backoffFactor)
	if newInterval > p.maxInterval {
		newInterval = p.maxInterval
	}
	p.currentInterval = newInterval
}

// fetchReadyIssues returns parsed ready issues via the IssueBackend.
// When parentID is non-empty, filters to tasks under that epic.
// When repoLabel is non-empty, filters to tasks labeled repo:<name>.
//
// One span per call (`automode.poll.cycle`) — one cycle of the poller, even
// when the result set is empty. The span ends when this function returns;
// downstream IssueBackend calls inherit it as parent.
func fetchReadyIssues(parentID string, repoLabel string) ([]backend.IssueData, error) {
	cycleStart := time.Now()
	ctx, span := startPollSpan(cmdstore.RootContext(), parentID, repoLabel)
	defer span.End()

	ib := cli.DefaultIssueBackend()
	// Limit 10000: ready queues include open + review + in_progress; a small limit
	// can push the few truly-workable open tasks past the cutoff, starving
	// auto-mode planners/implementers. Same pattern as monitor_collect.go.
	opts := backend.ReadyOpts{Limit: 10000, ParentID: parentID}
	if repoLabel != "" {
		opts.Labels = []string{"repo:" + repoLabel}
	}
	if sourceRepos := os.Getenv("LOOM_SOURCE_REPOS"); sourceRepos != "" {
		opts.SourceRepos = strings.Split(sourceRepos, ",")
	}
	issues, err := ib.Ready(ctx, opts)
	if err != nil {
		recordPollErr(span, err)
		span.SetAttributes(
			attribute.Int("result.count", 0),
			attribute.Int64("cycle.duration_ms", time.Since(cycleStart).Milliseconds()),
		)
		return nil, fmt.Errorf("failed to check ready tasks: %w", err)
	}
	span.SetAttributes(
		attribute.Int("result.count", len(issues)),
		attribute.Int64("cycle.duration_ms", time.Since(cycleStart).Milliseconds()),
	)
	return issues, nil
}

// GetAvailablePlanningTasks returns tasks that need planning
// (ready tasks without a design OR with needs-revision label, excluding epics)
// When parentID is non-empty, only tasks under that epic are returned.
// When repoLabel is non-empty, only tasks labeled repo:<name> are returned.
func GetAvailablePlanningTasks(parentID string, repoLabel string) ([]backend.IssueData, error) {
	candidates, err := fetchReadyIssues(parentID, repoLabel)
	if err != nil {
		return nil, err
	}

	var result []backend.IssueData
	for _, issue := range candidates {
		if cli.IsAvailableForPlanning(issue) {
			result = append(result, issue)
		}
	}
	return result, nil
}

// HasAvailablePlanningTasks checks if there are tasks that need planning
// (ready tasks without a design OR with needs-revision label, excluding epics)
func HasAvailablePlanningTasks(parentID string, repoLabel string) (bool, error) {
	tasks, err := GetAvailablePlanningTasks(parentID, repoLabel)
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}

// GetAvailableImplementationTasks returns tasks ready for implementation
// (ready tasks WITH an approved design, excluding tasks with needs-revision label and epics)
// When parentID is non-empty, only tasks under that epic are returned.
// When repoLabel is non-empty, only tasks labeled repo:<name> are returned.
func GetAvailableImplementationTasks(parentID string, repoLabel string) ([]backend.IssueData, error) {
	candidates, err := fetchReadyIssues(parentID, repoLabel)
	if err != nil {
		return nil, err
	}

	var result []backend.IssueData
	for _, issue := range candidates {
		if cli.IsAvailableForImplementation(issue) {
			result = append(result, issue)
		}
	}
	return result, nil
}

// HasAvailableImplementationTasks checks if there are tasks ready for implementation
// (ready tasks WITH an approved design, excluding tasks with needs-revision label and epics)
func HasAvailableImplementationTasks(parentID string, repoLabel string) (bool, error) {
	tasks, err := GetAvailableImplementationTasks(parentID, repoLabel)
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}

// GetAnyAvailableTasks returns any ready tasks regardless of design status.
// Used by custom roles with task_filter=any.
// When parentID is non-empty, only tasks under that epic are returned.
// When repoLabel is non-empty, only tasks labeled repo:<name> are returned.
func GetAnyAvailableTasks(parentID string, repoLabel string) ([]backend.IssueData, error) {
	candidates, err := fetchReadyIssues(parentID, repoLabel)
	if err != nil {
		return nil, err
	}

	var result []backend.IssueData
	for _, issue := range candidates {
		if cli.IsAvailableForAny(issue) {
			result = append(result, issue)
		}
	}
	return result, nil
}

// HasAnyAvailableTasks checks if there are any ready tasks regardless of design status.
// Used by custom roles with task_filter=any.
func HasAnyAvailableTasks(parentID string, repoLabel string) (bool, error) {
	tasks, err := GetAnyAvailableTasks(parentID, repoLabel)
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}
