package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
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
		currentInterval: 200 * time.Millisecond,  // Start at legacy value
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

// fetchReadyIssues runs "bd ready --json" and returns the parsed issues.
// When parentID is non-empty, filters to tasks under that epic.
func fetchReadyIssues(parentID string) ([]BdIssue, error) {
	args := []string{"bd", "ready", "--json", "--limit", "100"}
	if parentID != "" {
		args = append(args, "--parent", parentID)
	}
	result := execCommand(GetBeadsDir(), args[0], args[1:]...)
	if result.Err != nil {
		return nil, fmt.Errorf("failed to check ready tasks: %w", result.Err)
	}

	var issues []BdIssue
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse task list: %w", err)
	}
	return issues, nil
}

// GetAvailablePlanningTasks returns tasks that need planning
// (ready tasks without a design OR with needs-revision label, excluding epics)
// When parentID is non-empty, only tasks under that epic are returned.
func GetAvailablePlanningTasks(parentID string) ([]BdIssue, error) {
	issues, err := fetchReadyIssues(parentID)
	if err != nil {
		return nil, err
	}

	var candidates []BdIssue
	for _, issue := range issues {
		if IsAvailableForPlanning(issue, issues) {
			candidates = append(candidates, issue)
		}
	}
	return candidates, nil
}

// HasAvailablePlanningTasks checks if there are tasks that need planning
// (ready tasks without a design OR with needs-revision label, excluding epics)
func HasAvailablePlanningTasks(parentID string) (bool, error) {
	tasks, err := GetAvailablePlanningTasks(parentID)
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}

// GetAvailableImplementationTasks returns tasks ready for implementation
// (ready tasks WITH an approved design, excluding tasks with needs-revision label and epics)
// When parentID is non-empty, only tasks under that epic are returned.
func GetAvailableImplementationTasks(parentID string) ([]BdIssue, error) {
	issues, err := fetchReadyIssues(parentID)
	if err != nil {
		return nil, err
	}

	var candidates []BdIssue
	for _, issue := range issues {
		if IsAvailableForImplementation(issue, issues) {
			candidates = append(candidates, issue)
		}
	}
	return candidates, nil
}

// HasAvailableImplementationTasks checks if there are tasks ready for implementation
// (ready tasks WITH an approved design, excluding tasks with needs-revision label and epics)
func HasAvailableImplementationTasks(parentID string) (bool, error) {
	tasks, err := GetAvailableImplementationTasks(parentID)
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}

// GetAnyAvailableTasks returns any ready tasks regardless of design status.
// Used by custom roles with task_filter=any.
// When parentID is non-empty, only tasks under that epic are returned.
func GetAnyAvailableTasks(parentID string) ([]BdIssue, error) {
	issues, err := fetchReadyIssues(parentID)
	if err != nil {
		return nil, err
	}

	var candidates []BdIssue
	for _, issue := range issues {
		if IsAvailableForAny(issue, issues) {
			candidates = append(candidates, issue)
		}
	}
	return candidates, nil
}

// HasAnyAvailableTasks checks if there are any ready tasks regardless of design status.
// Used by custom roles with task_filter=any.
func HasAnyAvailableTasks(parentID string) (bool, error) {
	tasks, err := GetAnyAvailableTasks(parentID)
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}
