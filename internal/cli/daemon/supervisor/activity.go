package supervisor

import (
	"context"
	"hash/fnv"
	"sort"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
)

// Agent-IPC activity sinks, keyed by the agent's IPC identity (the worktree
// name), and task-progress evidence, keyed by issue. Both answer "is anything
// still moving?" — the first about a running agent, the second about the task
// it was working on. Tested in activity_test.go.

// findAgentByWorktree returns the supervised agent with the given worktree name,
// or nil when none matches. Shared by the agent-IPC sinks (RecordAgentActivity,
// RecordAgentInputWait), which are both keyed by the agent's IPC identity.
func (s *Supervisor) findAgentByWorktree(agentName string) *AgentProcess {
	s.AgentsMu.RLock()
	defer s.AgentsMu.RUnlock()
	for _, ap := range s.Agents {
		if ap.Entry.Worktree == agentName {
			return ap
		}
	}
	return nil
}

// RecordAgentActivity advances ap.LastActivity for the named agent toward the
// observed PTY-output timestamp. It is a no-op if the agent isn't currently
// supervised. Out-of-order heartbeats never regress the stored value — callers
// can safely retry without ever rewinding the timestamp.
func (s *Supervisor) RecordAgentActivity(agentName string, at time.Time) {
	if agentName == "" || at.IsZero() {
		return
	}
	target := s.findAgentByWorktree(agentName)
	if target == nil {
		return
	}
	target.Mu.Lock()
	if at.After(target.LastActivity) {
		target.LastActivity = at
	}
	target.Mu.Unlock()
}

// ---------------------------------------------------------------------------
// Task-progress evidence (keyed by issue)
// ---------------------------------------------------------------------------

// commitProgressed reports whether the worktree HEAD moved past the ref
// captured at session creation. An unknown baseline (BeforeRef empty — it is
// set only after session creation succeeds) or an unreadable current HEAD is
// NOT progress: comparing HEAD against "" would fake progress on every
// session-creation-failure exit and suppress quarantine for that failure mode.
func commitProgressed(worktreePath, beforeRef string) bool {
	if beforeRef == "" {
		return false
	}
	head := automode.CaptureHEADRef(worktreePath)
	return head != "" && head != beforeRef
}

// issueBaseline is the field-delta progress fingerprint of one issue, read
// off a single Get response — every component comes from data the GET already
// returned, so widening it costs no extra network call.
type issueBaseline struct {
	designHash   uint64
	notesHash    uint64
	maxCommentID int64
	labelsHash   uint64
}

// progressedFrom reports whether this (freshly read) baseline shows movement
// past the recorded one. Hashes compare by inequality; the comment id compares
// by > because it is monotone and a deletion must not read as progress.
func (b issueBaseline) progressedFrom(prev issueBaseline) bool {
	return b.designHash != prev.designHash ||
		b.notesHash != prev.notesHash ||
		b.maxCommentID > prev.maxCommentID ||
		b.labelsHash != prev.labelsHash
}

// issueBaselineOf fingerprints a Get response. Comments and Labels ride on the
// same IssueDetailData the design/notes hashes already came from.
func issueBaselineOf(issue *backend.IssueDetailData) issueBaseline {
	return issueBaseline{
		designHash:   hashIssueField(issue.Design),
		notesHash:    hashIssueField(issue.Notes),
		maxCommentID: maxCommentID(issue.Comments),
		labelsHash:   hashLabelSet(issue.Labels),
	}
}

// fetchIssueBaseline GETs the issue once per eligible kill and fingerprints
// it. ok=false (no backend, GET failed) means "unknown": the increment
// proceeds regardless, and the caller never compares against a zero baseline.
func (s *Supervisor) fetchIssueBaseline(taskID string) (base issueBaseline, ok bool) {
	if s.IssueBackend == nil {
		return issueBaseline{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), quarantineWriteTimeout)
	defer cancel()
	issue, err := s.IssueBackend.Get(ctx, taskID)
	if err != nil || issue == nil {
		return issueBaseline{}, false
	}
	return issueBaselineOf(issue), true
}

func hashIssueField(v string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(v))
	return h.Sum64()
}

// maxCommentID is the highest comment id on the issue (0 when there are
// none). fleet-db assigns comment ids monotonically, so this is edit-proof:
// editing a comment leaves the max alone, and only a NEW comment raises it.
func maxCommentID(comments []backend.CommentData) int64 {
	var highest int64
	for _, c := range comments {
		if c.ID > highest {
			highest = c.ID
		}
	}
	return highest
}

// hashLabelSet is FNV-1a over the sorted, NUL-delimited label set — order
// independent (label order is not meaningful) and unambiguous across
// concatenations.
func hashLabelSet(labels []string) uint64 {
	sorted := make([]string, len(labels))
	copy(sorted, labels)
	sort.Strings(sorted)
	h := fnv.New64a()
	for _, l := range sorted {
		_, _ = h.Write([]byte(l))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}
