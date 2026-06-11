package sessionfinalize

import (
	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/cli/git"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

type WithWorktreeOptions struct {
	WorktreePath string
	BeforeRef    string
	TaskID       string
	ExitCode     int
	ErrorClass   string

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
	applyTranscriptUsageFallback(sess, &opts)
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

// applyTranscriptUsageFallback populates zero token opts by summing the
// session's mirrored native transcript. Both live capture paths (the
// stream-json usage collector and the SessionEnd hook) can silently produce
// nothing; this guarantees non-zero tokens/cost at finalize whenever the
// transcript on disk carries usage. Caller-captured opts and hook-persisted
// disk values both stay authoritative: the fallback only runs when opts are
// all zero AND the on-disk metadata has no token usage (otherwise filling
// opts would demote the hook-captured values in Session.Finalize's merge,
// which prefers non-zero opts). Codex sessions are skipped — their rollout
// format is not the Claude transcript schema SumTranscriptUsage parses.
func applyTranscriptUsageFallback(sess *sessions.Session, opts *WithWorktreeOptions) {
	if opts.InputTokens != 0 || opts.OutputTokens != 0 ||
		opts.CacheReadTokens != 0 || opts.CacheWriteTokens != 0 {
		return
	}
	if sess.Meta.Backend == backendnames.Codex || sess.HasPersistedTokenUsage() {
		return
	}
	tok, err := sessions.SumTranscriptUsage(sess.NativeTranscriptPath())
	if err != nil {
		return
	}
	if tok.InputTokens == 0 && tok.OutputTokens == 0 &&
		tok.CacheReadTokens == 0 && tok.CacheWriteTokens == 0 {
		return
	}
	opts.InputTokens = tok.InputTokens
	opts.OutputTokens = tok.OutputTokens
	opts.CacheReadTokens = tok.CacheReadTokens
	opts.CacheWriteTokens = tok.CacheWriteTokens
	if opts.EstimatedCostUSD != 0 {
		return
	}
	tier := usage.ResolvePricing(sess.Meta.Backend)
	opts.EstimatedCostUSD = usage.EstimateCost(tier, usage.SessionUsage{
		InputTokens:      tok.InputTokens,
		OutputTokens:     tok.OutputTokens,
		CacheReadTokens:  tok.CacheReadTokens,
		CacheWriteTokens: tok.CacheWriteTokens,
	})
}
