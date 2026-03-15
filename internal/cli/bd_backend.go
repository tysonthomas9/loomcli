package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// bdBackend implements IssueTracker by delegating to a BDRunner (bd CLI).
type bdBackend struct {
	runner BDRunner
	dir    string
}

// newBDBackend creates a bdBackend wrapping the given BDRunner.
func newBDBackend(runner BDRunner, dir string) *bdBackend {
	return &bdBackend{runner: runner, dir: dir}
}

// RunCommand executes a bd CLI command and returns stdout.
// Implements IssueBackend.
func (b *bdBackend) RunCommand(dir string, args ...string) (string, error) {
	result := b.runner.Run(dir, args...)
	if result.Err != nil {
		return "", result.Err
	}
	return result.Stdout, nil
}

// --- Typed query methods ---

func (b *bdBackend) Ready(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
	args := []string{"ready", "--json"}
	if opts.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(opts.Limit))
	}
	if opts.ParentID != "" {
		args = append(args, "--parent", opts.ParentID)
	}
	return b.queryIssues(args...)
}

func (b *bdBackend) List(_ context.Context, opts ListOpts) ([]BdIssue, error) {
	args := []string{"list", "--json"}
	if opts.Status != "" {
		args = append(args, "--status="+opts.Status)
	}
	if opts.IssueType != "" {
		args = append(args, "--type="+opts.IssueType)
	}
	if opts.Assignee != "" {
		args = append(args, "--assignee", opts.Assignee)
	}
	if opts.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(opts.Limit))
	}
	return b.queryIssues(args...)
}

func (b *bdBackend) Blocked(_ context.Context) ([]BdIssue, error) {
	return b.queryIssues("blocked", "--json")
}

func (b *bdBackend) Stats(_ context.Context) (BdStats, error) {
	out, err := b.RunCommand(b.dir, "stats", "--json")
	if err != nil {
		return BdStats{}, fmt.Errorf("bd stats: %w", err)
	}
	var stats BdStats
	if err := json.Unmarshal([]byte(out), &stats); err != nil {
		return BdStats{}, fmt.Errorf("bd stats: parse: %w", err)
	}
	return stats, nil
}

// GetIssue returns a single issue by ID.
// bd show <id> --json returns a JSON array with one element.
func (b *bdBackend) GetIssue(_ context.Context, id string) (*BdIssue, error) {
	out, err := b.RunCommand(b.dir, "show", id, "--json")
	if err != nil {
		return nil, fmt.Errorf("bd show %s: %w", id, err)
	}
	var issues []BdIssue
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		return nil, fmt.Errorf("bd show %s: parse: %w", id, err)
	}
	if len(issues) == 0 {
		return nil, fmt.Errorf("bd show %s: no results", id)
	}
	return &issues[0], nil
}

func (b *bdBackend) GetIssueText(_ context.Context, id string) (string, error) {
	out, err := b.RunCommand(b.dir, "show", id)
	if err != nil {
		return "", fmt.Errorf("bd show %s: %w", id, err)
	}
	return strings.TrimSpace(out), nil
}

// --- Mutation methods ---

func (b *bdBackend) UpdateStatus(_ context.Context, id, status, assignee string) error {
	args := []string{"update", id, "--status", status}
	if assignee != "" {
		args = append(args, "--assignee", assignee)
	}
	_, err := b.RunCommand(b.dir, args...)
	if err != nil {
		return fmt.Errorf("bd update %s: %w", id, err)
	}
	return nil
}

func (b *bdBackend) UpdateExternalRef(_ context.Context, id, ref string) error {
	_, err := b.RunCommand(b.dir, "update", id, "--external-ref", ref)
	if err != nil {
		return fmt.Errorf("bd update %s --external-ref: %w", id, err)
	}
	return nil
}

func (b *bdBackend) CloseIssue(_ context.Context, id, reason string) error {
	args := []string{"close", id}
	if reason != "" {
		args = append(args, "--reason", reason)
	}
	_, err := b.RunCommand(b.dir, args...)
	if err != nil {
		return fmt.Errorf("bd close %s: %w", id, err)
	}
	return nil
}

func (b *bdBackend) SyncStatus(_ context.Context) (string, error) {
	out, err := b.RunCommand(b.dir, "sync", "--status")
	if err != nil {
		return "", fmt.Errorf("bd sync: %w", err)
	}
	return strings.TrimSpace(out), nil
}

func (b *bdBackend) BackendName() string {
	return "beads"
}

// --- helpers ---

// queryIssues runs a bd command that returns a JSON array of BdIssue.
func (b *bdBackend) queryIssues(args ...string) ([]BdIssue, error) {
	out, err := b.RunCommand(b.dir, args...)
	if err != nil {
		return nil, fmt.Errorf("bd %s: %w", args[0], err)
	}
	var issues []BdIssue
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		return nil, fmt.Errorf("bd %s: parse: %w", args[0], err)
	}
	return issues, nil
}
