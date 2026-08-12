package sessionfinalize

import (
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/git"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

type localSession interface {
	SessionID() string
	Backend() string
	StartedAt() time.Time
	Finalize(sessions.FinalizeOptions) error
	SyncLatestCodexRollout(string, time.Time) (string, error)
	SyncLatestClaudeTranscript(string, string, time.Time) (string, error)
}

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

func WithWorktree(sess localSession, opts WithWorktreeOptions) (WithWorktreeResult, error) {
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
	syncNativeTranscript(sess, opts)
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

func syncNativeTranscript(sess localSession, opts WithWorktreeOptions) {
	if sess.Backend() == platformruntime.ProviderCodex {
		path, err := sess.SyncLatestCodexRollout(opts.WorktreePath, sess.StartedAt())
		if err != nil {
			slog.Warn("codex transcript sync failed",
				"session_id", sess.SessionID(),
				"worktree", opts.WorktreePath,
				"err", err,
			)
		} else if path == "" {
			slog.Warn("codex transcript unavailable after run",
				"session_id", sess.SessionID(),
				"worktree", opts.WorktreePath,
			)
		}
	}
	if sess.Backend() == platformruntime.ProviderClaude {
		_, _ = sess.SyncLatestClaudeTranscript(opts.WorktreePath, opts.ClaudeSessionID, sess.StartedAt())
	}
}
