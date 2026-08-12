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
		// Go-leaf daemon finalize often arrives with zeros because the worker was
		// reaped before collector finalize. Recover usage from the synced rollout.
		opts = recoverUsageFromNativeTranscript(sess, opts)
	}
	if sess.Meta.Backend == backendnames.Claude {
		_, _ = sess.SyncLatestClaudeTranscript(opts.WorktreePath, opts.ClaudeSessionID, sess.Meta.StartedAt)
		opts = recoverUsageFromNativeTranscript(sess, opts)
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

func recoverUsageFromNativeTranscript(sess *sessions.Session, opts WithWorktreeOptions) WithWorktreeOptions {
	if opts.InputTokens != 0 || opts.OutputTokens != 0 ||
		opts.CacheReadTokens != 0 || opts.CacheWriteTokens != 0 {
		return opts
	}
	u := sess.TranscriptUsage()
	if u.IsZero() {
		return opts
	}
	opts.InputTokens = u.InputTokens
	opts.OutputTokens = u.OutputTokens
	opts.CacheReadTokens = u.CacheReadTokens
	opts.CacheWriteTokens = u.CacheWriteTokens
	return opts
}
