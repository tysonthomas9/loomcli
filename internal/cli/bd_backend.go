package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// bdBackend implements IssueTracker by shelling out to the bd CLI via BDRunner.
type bdBackend struct {
	runner BDRunner
	dir    string
}

// Compile-time interface check.
var _ IssueTracker = (*bdBackend)(nil)

func newBdBackend(runner BDRunner, dir string) *bdBackend {
	return &bdBackend{runner: runner, dir: dir}
}

// --- IssueBackend (Phase 1 shim) ---

func (b *bdBackend) RunCommand(dir string, args ...string) (string, error) {
	result := b.runner.Run(dir, args...)
	if result.Err != nil {
		return "", result.Err
	}
	return result.Stdout, nil
}

// --- Query methods ---

func (b *bdBackend) Ready(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
	args := []string{"ready", "--json"}
	if opts.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(opts.Limit))
	}
	if opts.ParentID != "" {
		args = append(args, "--parent", opts.ParentID)
	}
	for _, label := range opts.Labels {
		args = append(args, "--label", label)
	}
	if len(opts.SourceRepos) > 0 {
		args = append(args, "--source-repos="+strings.Join(opts.SourceRepos, ","))
	}
	return b.queryIssues("Ready", args)
}

func (b *bdBackend) List(_ context.Context, opts ListOpts) ([]BdIssue, error) {
	args := []string{"list", "--json"}
	if opts.Status != "" {
		args = append(args, "--status", opts.Status)
	}
	if opts.Assignee != "" {
		args = append(args, "--assignee", opts.Assignee)
	}
	if opts.Type != "" {
		args = append(args, "--type", opts.Type)
	}
	if opts.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(opts.Limit))
	}
	return b.queryIssues("List", args)
}

func (b *bdBackend) Blocked(_ context.Context) ([]BdIssue, error) {
	return b.queryIssues("Blocked", []string{"blocked", "--json"})
}

func (b *bdBackend) Stats(_ context.Context) (*BdStats, error) {
	result := b.runner.Run(b.dir, "stats", "--json")
	if result.Err != nil {
		return nil, fmt.Errorf("bdBackend.Stats: %w: %s", result.Err, strings.TrimSpace(result.Stderr))
	}
	var stats BdStats
	if err := json.Unmarshal([]byte(result.Stdout), &stats); err != nil {
		return nil, fmt.Errorf("bdBackend.Stats: parse: %w", err)
	}
	return &stats, nil
}

func (b *bdBackend) GetIssue(_ context.Context, id string) (*BdIssue, error) {
	result := b.runner.Run(b.dir, "show", id, "--json")
	if result.Err != nil {
		return nil, fmt.Errorf("bdBackend.GetIssue(%s): %w: %s", id, result.Err, strings.TrimSpace(result.Stderr))
	}
	// bd show --json returns a single-element array
	var issues []BdIssue
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return nil, fmt.Errorf("bdBackend.GetIssue(%s): parse: %w", id, err)
	}
	if len(issues) == 0 {
		return nil, fmt.Errorf("bdBackend.GetIssue(%s): not found", id)
	}
	return &issues[0], nil
}

func (b *bdBackend) GetIssueText(_ context.Context, id string) (string, error) {
	result := b.runner.Run(b.dir, "show", id)
	if result.Err != nil {
		return "", fmt.Errorf("bdBackend.GetIssueText(%s): %w: %s", id, result.Err, strings.TrimSpace(result.Stderr))
	}
	return result.Stdout, nil
}

// --- Mutation methods ---

func (b *bdBackend) UpdateIssue(_ context.Context, id string, opts UpdateOpts) error {
	args := []string{"update", id}
	if opts.Status != "" {
		args = append(args, "--status", opts.Status)
	}
	if opts.Assignee != nil {
		args = append(args, "--assignee", *opts.Assignee)
	}
	if opts.Design != "" {
		args = append(args, "--design", opts.Design)
	}
	if opts.Claim {
		args = append(args, "--claim")
	}
	return b.runMutation("UpdateIssue", args...)
}

func (b *bdBackend) UpdateExternalRef(_ context.Context, id, ref string) error {
	return b.runMutation("UpdateExternalRef", "update", id, "--external-ref", ref)
}

func (b *bdBackend) CloseIssue(_ context.Context, id, reason string) error {
	return b.runMutation("CloseIssue", "close", id, "--reason", reason)
}

// --- Metadata ---

func (b *bdBackend) BackendName() string {
	return "beads"
}

// --- internal helpers ---

func (b *bdBackend) queryIssues(method string, args []string) ([]BdIssue, error) {
	result := b.runner.Run(b.dir, args...)
	if result.Err != nil {
		return nil, fmt.Errorf("bdBackend.%s: %w: %s", method, result.Err, strings.TrimSpace(result.Stderr))
	}
	var issues []BdIssue
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return nil, fmt.Errorf("bdBackend.%s: parse: %w", method, err)
	}
	return issues, nil
}

func (b *bdBackend) runMutation(method string, args ...string) error {
	result := b.runner.Run(b.dir, args...)
	if result.Err != nil {
		return fmt.Errorf("bdBackend.%s: %w: %s", method, result.Err, strings.TrimSpace(result.Stderr))
	}
	return nil
}
