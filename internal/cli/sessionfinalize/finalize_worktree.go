// Package sessionfinalize closes out one agent run's session record: it
// computes the worktree's git diff stats and patch against a before-ref, pulls
// the backend-native transcript (codex rollout or claude transcript) into the
// session, and writes exit code, error class, token usage, and cost through
// sessions.Finalize. Called by internal/cli/agent, internal/cli/automode, and
// the daemon supervisor at the end of every task attempt.
package sessionfinalize

import (
	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/cli/git"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

type WithWorktreeOptions struct {
	WorktreePath string
	BeforeRef    string
	TaskID       string
	ExitCode     int
	ErrorClass   string

	// ClaudeSessionID is the Claude Code session UUID captured from the run's
	// stream output. Used to resolve the native transcript exactly; empty
	// falls back to newest-by-mtime in the worktree's project dir.
	ClaudeSessionID string

	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	EstimatedCostUSD float64
}

type WithWorktreeResult struct {
	DiffStats    sessions.DiffStats
	FilesTouched []string
	HasDiffPatch bool
}

func WithWorktree(sess *sessions.Session, opts WithWorktreeOptions) (WithWorktreeResult, error) {
	gitStats := git.ComputeDiffStats(opts.WorktreePath, opts.BeforeRef)
	result := WithWorktreeResult{
		DiffStats: sessions.DiffStats{
			FilesChanged: gitStats.FilesChanged,
			LinesAdded:   gitStats.LinesAdded,
			LinesRemoved: gitStats.LinesRemoved,
		},
		FilesTouched: gitStats.FilesTouched,
	}

	diffPatch := ""
	if gitStats.FilesChanged > 0 {
		diffPatch = git.ComputeDiffPatch(opts.WorktreePath, opts.BeforeRef)
		result.HasDiffPatch = diffPatch != ""
	}

	if sess == nil {
		return result, nil
	}
	if sess.Meta.Backend == backendnames.Codex {
		_, _ = sess.SyncLatestCodexRollout(opts.WorktreePath, sess.Meta.StartedAt)
	}
	if sess.Meta.Backend == backendnames.Claude {
		_, _ = sess.SyncLatestClaudeTranscript(opts.WorktreePath, opts.ClaudeSessionID, sess.Meta.StartedAt)
	}
	return result, sess.Finalize(sessions.FinalizeOptions{
		TaskID:       opts.TaskID,
		ExitCode:     opts.ExitCode,
		ErrorClass:   opts.ErrorClass,
		FilesTouched: result.FilesTouched,
		DiffPatch:    diffPatch,
		DiffStats:    result.DiffStats,

		InputTokens:      opts.InputTokens,
		OutputTokens:     opts.OutputTokens,
		CacheReadTokens:  opts.CacheReadTokens,
		CacheWriteTokens: opts.CacheWriteTokens,
		EstimatedCostUSD: opts.EstimatedCostUSD,
	})
}
