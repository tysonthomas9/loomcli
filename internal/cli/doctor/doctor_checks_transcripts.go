package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// transcriptMatchMargin tolerates clock skew between a session's recorded start
// and the first write to its Claude Code transcript.
const transcriptMatchMargin = 2 * time.Minute

type orphanSession struct {
	sessionID string
	agentName string
	backend   string
	startedAt time.Time
}

// checkOrphanedTranscripts finds completed Claude sessions whose native
// transcript was never captured — e.g. fleet/daemon-mode runs, which have no
// Claude Code hooks installed — and, with --fix, backfills agent_transcript.jsonl
// plus token usage from Claude Code's own ~/.claude/projects transcript.
func checkOrphanedTranscripts() CheckResult {
	sessStore, err := sessions.NewStore(cli.GetWorkspaceRuntimeDir())
	if err != nil {
		return CheckResult{} // skip — sessions store not available
	}
	orphans, err := scanOrphanedClaudeSessions(sessStore)
	if err != nil {
		return CheckResult{
			Name:    "orphaned_transcripts",
			Status:  StatusWarn,
			Summary: "could not scan sessions for missing transcripts",
			Detail:  err.Error(),
		}
	}
	if len(orphans) == 0 {
		return CheckResult{
			Name:    "orphaned_transcripts",
			Status:  StatusPass,
			Summary: "no orphaned claude transcripts",
		}
	}
	if doctorFix {
		return fixOrphanedTranscripts(sessStore, orphans)
	}
	return CheckResult{
		Name:    "orphaned_transcripts",
		Status:  StatusWarn,
		Summary: fmt.Sprintf("%d claude session(s) missing a captured transcript", len(orphans)),
		Detail:  "Run: loom doctor --fix to backfill from ~/.claude/projects",
	}
}

// scanOrphanedClaudeSessions returns claude-backend sessions whose
// agent_transcript.jsonl is absent.
func scanOrphanedClaudeSessions(store *sessions.Store) ([]orphanSession, error) {
	entries, err := os.ReadDir(store.Dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var orphans []orphanSession
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		meta, loadErr := store.LoadMetadata(id)
		if loadErr != nil || meta.Backend != backendnames.Claude {
			continue
		}
		if meta.Status == sessions.StatusRunning {
			continue
		}
		if info, statErr := os.Stat(store.NativeTranscriptPath(id)); statErr == nil && !info.IsDir() && info.Size() > 0 {
			continue // transcript already present with content
		}
		orphans = append(orphans, orphanSession{
			sessionID: id,
			agentName: meta.AgentName,
			backend:   meta.Backend,
			startedAt: meta.StartedAt,
		})
	}
	return orphans, nil
}

// fixOrphanedTranscripts correlates each orphan to a Claude Code transcript by
// agent name + start time and backfills the transcript and token usage. Agents
// run sequentially per worktree (lock-serialized), so matching the earliest
// session to the earliest unclaimed transcript lines runs up reliably.
func fixOrphanedTranscripts(store *sessions.Store, orphans []orphanSession) CheckResult {
	workspaceToken := filepath.Base(cli.GetWorkspaceRuntimeDir())
	claimed := make(map[string]bool)
	fixed, unmatched := 0, 0
	var details []string

	sort.Slice(orphans, func(i, j int) bool { return orphans[i].startedAt.Before(orphans[j].startedAt) })

	for _, o := range orphans {
		candidates := claudeCandidateFiles(workspaceToken, o.agentName)
		src := earliestUnclaimed(candidates, claimed, o.startedAt)
		if src == "" {
			unmatched++
			details = append(details, fmt.Sprintf("unmatched: %s (%s)", o.sessionID, o.agentName))
			continue
		}
		claimed[src] = true
		if err := backfillSession(store, o, src); err != nil {
			details = append(details, fmt.Sprintf("failed: %s: %v", o.sessionID, err))
			continue
		}
		fixed++
		details = append(details, fmt.Sprintf("backfilled: %s ← %s", o.sessionID, filepath.Base(src)))
	}

	status := StatusPass
	if unmatched > 0 {
		status = StatusWarn
	}
	return CheckResult{
		Name:    "orphaned_transcripts",
		Status:  status,
		Summary: fmt.Sprintf("backfilled %d transcript(s), %d unmatched", fixed, unmatched),
		Detail:  strings.Join(details, "\n"),
	}
}

// backfillSession mirrors the transcript into the session and records token
// usage, mirroring the hook-based captureTokenUsage path.
func backfillSession(store *sessions.Store, o orphanSession, srcPath string) error {
	if err := store.SyncNativeTranscript(o.sessionID, srcPath); err != nil {
		return err
	}
	tok, err := sessions.SumTranscriptUsage(srcPath)
	if err != nil {
		return err
	}
	if tok.InputTokens == 0 && tok.OutputTokens == 0 &&
		tok.CacheReadTokens == 0 && tok.CacheWriteTokens == 0 {
		return nil // transcript captured; no usage to record
	}
	meta, err := store.LoadMetadata(o.sessionID)
	if err != nil {
		return err
	}
	meta.InputTokens = tok.InputTokens
	meta.OutputTokens = tok.OutputTokens
	meta.CacheReadTokens = tok.CacheReadTokens
	meta.CacheWriteTokens = tok.CacheWriteTokens
	meta.EstimatedCostUSD = usage.EstimateCost(usage.ResolvePricing(o.backend), usage.SessionUsage{
		InputTokens:      tok.InputTokens,
		OutputTokens:     tok.OutputTokens,
		CacheReadTokens:  tok.CacheReadTokens,
		CacheWriteTokens: tok.CacheWriteTokens,
	})
	if err := store.SaveMetadata(o.sessionID, meta); err != nil {
		return err
	}
	return store.ReIndex(meta.SessionRecord)
}

type transcriptCandidate struct {
	path  string
	mtime time.Time
}

// claudeCandidateFiles returns Claude Code transcript files for agentName under
// the active workspace. Claude indexes transcripts by encoded working dir; the
// project dir name ends with "-<agentName>" and contains the workspace token.
func claudeCandidateFiles(workspaceToken, agentName string) []transcriptCandidate {
	root := claudeProjectsRoot()
	if root == "" || agentName == "" {
		return nil
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	suffix := "-" + agentName
	var out []transcriptCandidate
	for _, d := range dirs {
		if !d.IsDir() || !strings.HasSuffix(d.Name(), suffix) {
			continue
		}
		if workspaceToken != "" && !strings.Contains(d.Name(), workspaceToken) {
			continue
		}
		out = append(out, readProjectTranscripts(filepath.Join(root, d.Name()))...)
	}
	return out
}

// readProjectTranscripts lists the *.jsonl transcripts in a Claude project dir.
func readProjectTranscripts(projectDir string) []transcriptCandidate {
	files, err := os.ReadDir(projectDir)
	if err != nil {
		return nil
	}
	var out []transcriptCandidate
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		info, ierr := f.Info()
		if ierr != nil {
			continue
		}
		out = append(out, transcriptCandidate{path: filepath.Join(projectDir, f.Name()), mtime: info.ModTime()})
	}
	return out
}

// earliestUnclaimed returns the unclaimed candidate with the smallest mtime at
// or after startedAt (minus a skew margin), or "" if none qualifies.
func earliestUnclaimed(candidates []transcriptCandidate, claimed map[string]bool, startedAt time.Time) string {
	cutoff := startedAt.Add(-transcriptMatchMargin)
	best := ""
	var bestMod time.Time
	for _, c := range candidates {
		if claimed[c.path] || c.mtime.Before(cutoff) {
			continue
		}
		if best == "" || c.mtime.Before(bestMod) {
			best = c.path
			bestMod = c.mtime
		}
	}
	return best
}

// claudeProjectsRoot returns ~/.claude/projects, or "" if home is unavailable.
func claudeProjectsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}
