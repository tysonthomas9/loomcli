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
// When repoLabel is non-empty, filters to tasks labeled repo:<name>.
// It routes through the package-level defaultDeps.BD runner.
func fetchReadyIssues(parentID string, repoLabel string) ([]BdIssue, error) {
	args := []string{"ready", "--json", "--limit", "100"}
	if parentID != "" {
		args = append(args, "--parent", parentID)
	}
	if repoLabel != "" {
		args = append(args, "--label", "repo:"+repoLabel)
	}
	result := defaultDeps.BD.Run(GetBeadsDir(), args...)
	if result.Err != nil {
		return nil, fmt.Errorf("failed to check ready tasks: %w", result.Err)
	}

	var issues []BdIssue
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse task list: %w", err)
	}
	return issues, nil
}

// fetchUnclosedIssueIDs returns IDs of all issues that are NOT closed.
// Used for accurate blocker checking — a blocker is resolved only when closed,
// not when it merely moves to in_progress or review.
// It routes through the package-level defaultDeps.BD runner.
func fetchUnclosedIssueIDs() (map[string]bool, error) {
	result := defaultDeps.BD.Run(GetBeadsDir(), "list", "--json", "--limit", "500")
	if result.Err != nil {
		return nil, fmt.Errorf("failed to list issues: %w", result.Err)
	}

	var issues []BdIssue
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse issue list: %w", err)
	}
	ids := make(map[string]bool, len(issues))
	for _, issue := range issues {
		if issue.Status != "closed" {
			ids[issue.ID] = true
		}
	}
	return ids, nil
}

// GetAvailablePlanningTasks returns tasks that need planning
// (ready tasks without a design OR with needs-revision label, excluding epics)
// When parentID is non-empty, only tasks under that epic are returned.
// When repoLabel is non-empty, only tasks labeled repo:<name> are returned.
func GetAvailablePlanningTasks(parentID string, repoLabel string) ([]BdIssue, error) {
	candidates, err := fetchReadyIssues(parentID, repoLabel)
	if err != nil {
		return nil, err
	}
	unclosedIDs, err := fetchUnclosedIssueIDs()
	if err != nil {
		return nil, err
	}

	var result []BdIssue
	for _, issue := range candidates {
		if IsAvailableForPlanning(issue, unclosedIDs) {
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
func GetAvailableImplementationTasks(parentID string, repoLabel string) ([]BdIssue, error) {
	candidates, err := fetchReadyIssues(parentID, repoLabel)
	if err != nil {
		return nil, err
	}
	unclosedIDs, err := fetchUnclosedIssueIDs()
	if err != nil {
		return nil, err
	}

	var result []BdIssue
	for _, issue := range candidates {
		if IsAvailableForImplementation(issue, unclosedIDs) {
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
func GetAnyAvailableTasks(parentID string, repoLabel string) ([]BdIssue, error) {
	candidates, err := fetchReadyIssues(parentID, repoLabel)
	if err != nil {
		return nil, err
	}
	unclosedIDs, err := fetchUnclosedIssueIDs()
	if err != nil {
		return nil, err
	}

	var result []BdIssue
	for _, issue := range candidates {
		if IsAvailableForAny(issue, unclosedIDs) {
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

// BuildRouterTaskCheck creates a CustomTaskCheck function that uses the task router's
// SelectBestTask instead of the generic Has*Tasks functions. Returns nil if the role
// has no routing constraints (Skills, MaxPriority, and TaskFilter all unset), signaling
// the caller to use default task checking.
//
// Wire-up: The daemon passes routing constraints as env vars; the subprocess (loom agent)
// reconstructs the RoleConfig and calls this function when setting up AutoModeOptions.
// Consumer-side integration is tracked separately.
func BuildRouterTaskCheck(rc RoleConfig, ae AgentEntry, parentID string) func() (bool, error) {
	constraints := MergeRoleConstraints(rc, ae)
	repoLabel := ae.Repo
	// If no routing fields are set, return nil to use defaults
	if len(constraints.Skills) == 0 && constraints.MaxPriority == nil && constraints.TaskFilter == "" && repoLabel == "" {
		return nil
	}
	return func() (bool, error) {
		issues, err := fetchReadyIssues(parentID, repoLabel)
		if err != nil {
			return false, err
		}
		unclosedIDs, err := fetchUnclosedIssueIDs()
		if err != nil {
			return false, err
		}
		match := SelectBestTask(issues, constraints, unclosedIDs)
		return match != nil, nil
	}
}
