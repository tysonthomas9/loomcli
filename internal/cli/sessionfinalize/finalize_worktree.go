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
	Model            string
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
	switch sess.Meta.Backend {
	case backendnames.Codex:
		enrichCodexUsageFromTranscript(sess, &opts)
	case backendnames.Claude:
		enrichClaudeUsageFromTranscript(sess, &opts)
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
		Model:            opts.Model,
	})
}

func enrichCodexUsageFromTranscript(sess *sessions.Session, opts *WithWorktreeOptions) {
	srcPath, _ := sess.SyncLatestCodexRollout(opts.WorktreePath, sess.Meta.StartedAt)
	enrichUsageFromTranscript(srcPath, opts)
}

func enrichClaudeUsageFromTranscript(sess *sessions.Session, opts *WithWorktreeOptions) {
	srcPath, _ := sess.SyncLatestClaudeTranscript(opts.WorktreePath, opts.ClaudeSessionID, sess.Meta.StartedAt)
	enrichUsageFromTranscript(srcPath, opts)
}

func enrichUsageFromTranscript(srcPath string, opts *WithWorktreeOptions) {
	if srcPath == "" {
		return
	}
	tok, err := sessions.SumTranscriptUsage(srcPath)
	if err != nil {
		return
	}
	applyTranscriptUsage(opts, tok)
}

func applyTranscriptUsage(opts *WithWorktreeOptions, tok sessions.TokenUsage) {
	if opts.InputTokens == 0 {
		opts.InputTokens = tok.InputTokens
	}
	if opts.OutputTokens == 0 {
		opts.OutputTokens = tok.OutputTokens
	}
	if opts.CacheReadTokens == 0 {
		opts.CacheReadTokens = tok.CacheReadTokens
	}
	if opts.CacheWriteTokens == 0 {
		opts.CacheWriteTokens = tok.CacheWriteTokens
	}
	if tok.CostUSD > 0 {
		opts.EstimatedCostUSD = tok.CostUSD
	}
	if tok.Model != "" {
		opts.Model = tok.Model
	}
}
