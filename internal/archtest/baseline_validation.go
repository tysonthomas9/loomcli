package archtest

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

var validationEnvironmentName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

var requiredPhase0ValidationRuns = []string{
	"fleetdb-gate",
	"fleetdb-supervisor",
	"local-mode-codex-verify",
	"loom-gate",
	"openapi-snapshot-match",
}

// ValidationBaseline is the machine-readable Phase 0 hard-gate evidence.
// Snapshots remain immutable: a newer validation adds another source-bound
// record rather than relabeling commands or artifacts from an older tree.
type ValidationBaseline struct {
	Snapshots []ValidationSnapshot `json:"snapshots"`
}

type ValidationSnapshot struct {
	ID            string          `json:"id"`
	LoomHead      string          `json:"loom_head"`
	FleetDBHead   string          `json:"fleetdb_head"`
	OpenAPISHA256 string          `json:"openapi_sha256"`
	RecordedAt    string          `json:"recorded_at"`
	Runs          []ValidationRun `json:"runs"`
}

type ValidationRun struct {
	ID            string                `json:"id"`
	Repository    string                `json:"repository"`
	Command       []string              `json:"command"`
	Environment   ValidationEnvironment `json:"environment"`
	ExpectedSkips []ValidationSkip      `json:"expected_skips"`
	Result        ValidationResult      `json:"result"`
	ArtifactPaths []string              `json:"artifact_paths"`
}

type ValidationEnvironment struct {
	Set   []string `json:"set"`
	Unset []string `json:"unset"`
	Notes string   `json:"notes,omitempty"`
}

type ValidationSkip struct {
	Name   string `json:"name"`
	Owner  string `json:"owner"`
	Reason string `json:"reason"`
}

type ValidationResult struct {
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

func validateValidationBaseline(validation ValidationBaseline, source SourceBaseline) error {
	if len(validation.Snapshots) == 0 {
		return errors.New("baseline validation must record at least one immutable snapshot")
	}
	ids := make([]string, 0, len(validation.Snapshots))
	currentFound := false
	for index, snapshot := range validation.Snapshots {
		if err := validateValidationSnapshot(snapshot); err != nil {
			return fmt.Errorf("baseline validation snapshots[%d]: %w", index, err)
		}
		ids = append(ids, snapshot.ID)
		if snapshot.LoomHead == source.LoomHead && snapshot.FleetDBHead == source.FleetDBHead &&
			snapshot.OpenAPISHA256 == source.FleetDBOpenAPISHA256 {
			if err := validateCurrentPhase0Runs(snapshot.Runs); err != nil {
				return fmt.Errorf("baseline validation current source snapshot %q: %w", snapshot.ID, err)
			}
			currentFound = true
		}
	}
	if err := validateSortedUnique("baseline validation snapshot id", ids); err != nil {
		return err
	}
	if !currentFound {
		return errors.New("baseline validation must include a passing snapshot matching the current Loom, FleetDB, and OpenAPI source baseline")
	}
	return nil
}

func validateValidationSnapshot(snapshot ValidationSnapshot) error {
	if strings.TrimSpace(snapshot.ID) == "" || snapshot.ID != strings.TrimSpace(snapshot.ID) {
		return errors.New("id must be a non-empty trimmed value")
	}
	if !fullSHA.MatchString(snapshot.LoomHead) || !fullSHA.MatchString(snapshot.FleetDBHead) {
		return errors.New("loom_head and fleetdb_head must be full lowercase Git SHAs")
	}
	if !sha256.MatchString(snapshot.OpenAPISHA256) {
		return errors.New("openapi_sha256 must be a lowercase SHA-256")
	}
	if !validValidationDate(snapshot.RecordedAt) {
		return errors.New("recorded_at must be an ISO date or RFC3339 timestamp")
	}
	if len(snapshot.Runs) == 0 {
		return errors.New("runs must not be empty")
	}
	ids := make([]string, 0, len(snapshot.Runs))
	for index, run := range snapshot.Runs {
		if err := validateValidationRun(run); err != nil {
			return fmt.Errorf("runs[%d]: %w", index, err)
		}
		ids = append(ids, run.ID)
	}
	return validateSortedUnique("baseline validation run id", ids)
}

func validateValidationRun(run ValidationRun) error {
	if strings.TrimSpace(run.ID) == "" || run.ID != strings.TrimSpace(run.ID) {
		return errors.New("id must be a non-empty trimmed value")
	}
	if !slices.Contains([]string{"cross-repo", "fleetdb", "loom"}, run.Repository) {
		return fmt.Errorf("run %q repository %q must be cross-repo, fleetdb, or loom", run.ID, run.Repository)
	}
	if len(run.Command) == 0 {
		return fmt.Errorf("run %q command must not be empty", run.ID)
	}
	for index, arg := range run.Command {
		if strings.TrimSpace(arg) == "" || strings.ContainsAny(arg, "\r\n") {
			return fmt.Errorf("run %q command[%d] must be a non-empty single-line argument", run.ID, index)
		}
	}
	if err := validateValidationEnvironment(run.ID, run.Environment); err != nil {
		return err
	}
	if run.ExpectedSkips == nil || run.ArtifactPaths == nil {
		return fmt.Errorf("run %q must explicitly record expected_skips and artifact_paths", run.ID)
	}
	skipNames := make([]string, 0, len(run.ExpectedSkips))
	for _, skip := range run.ExpectedSkips {
		if strings.TrimSpace(skip.Name) == "" || strings.TrimSpace(skip.Owner) == "" || strings.TrimSpace(skip.Reason) == "" {
			return fmt.Errorf("run %q every expected skip requires name, owner, and reason", run.ID)
		}
		skipNames = append(skipNames, skip.Name)
	}
	if err := validateSortedUnique("baseline validation expected skip", skipNames); err != nil {
		return err
	}
	if !slices.Contains([]string{"expected-red", "not-recorded", "pass"}, run.Result.Status) || strings.TrimSpace(run.Result.Summary) == "" {
		return fmt.Errorf("run %q result requires status pass, expected-red, or not-recorded and a summary", run.ID)
	}
	for index, path := range run.ArtifactPaths {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
		if strings.TrimSpace(path) == "" || strings.ContainsAny(path, "\r\n") || clean != path || strings.HasPrefix(path, "../") {
			return fmt.Errorf("run %q artifact_paths[%d] must be a clean non-empty path", run.ID, index)
		}
	}
	return nil
}

func validateValidationEnvironment(runID string, environment ValidationEnvironment) error {
	if environment.Set == nil || environment.Unset == nil {
		return fmt.Errorf("run %q environment must explicitly record set and unset", runID)
	}
	setNames := make([]string, 0, len(environment.Set))
	for index, entry := range environment.Set {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || !validationEnvironmentName.MatchString(name) {
			return fmt.Errorf("run %q environment.set[%d] must be NAME=value", runID, index)
		}
		setNames = append(setNames, name)
	}
	if err := validateSortedUnique("baseline validation set environment name", setNames); err != nil {
		return err
	}
	if err := validateSortedUnique("baseline validation unset environment name", environment.Unset); err != nil {
		return err
	}
	for _, name := range environment.Unset {
		if !validationEnvironmentName.MatchString(name) || slices.Contains(setNames, name) {
			return fmt.Errorf("run %q environment unset name %q is invalid or also set", runID, name)
		}
	}
	return nil
}

func validateCurrentPhase0Runs(runs []ValidationRun) error {
	byID := make(map[string]ValidationRun, len(runs))
	for _, run := range runs {
		byID[run.ID] = run
	}
	for _, id := range requiredPhase0ValidationRuns {
		run, ok := byID[id]
		if !ok {
			return fmt.Errorf("missing required hard-gate run %q", id)
		}
		if run.Result.Status != "pass" {
			return fmt.Errorf("hard-gate run %q has status %q, want pass", id, run.Result.Status)
		}
	}
	return nil
}

func validValidationDate(value string) bool {
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return true
	}
	_, err := time.Parse(time.DateOnly, value)
	return err == nil
}
