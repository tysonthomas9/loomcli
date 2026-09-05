package leadcontrol

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// ResumeLatestSentinel is the --resume value that means "the most recent
// resumable session", i.e. exactly what --continue does. It is registered as
// the flag's NoOptDefVal so a bare `--resume` behaves like `--continue`.
const ResumeLatestSentinel = "latest"

// defaultLeadSessionListLimit caps the orchestration rows a resume scan pulls
// back. Resume only ever needs the newest handful; the limit keeps a lead with
// a long history from paging the whole table on every launch.
const defaultLeadSessionListLimit = 50

// leadLivenessWindow is how fresh a heartbeat must be for a `running` row to
// count as still live. It mirrors 2x the lead heartbeat interval in
// internal/cli/agent/lead (30s); duplicated rather than imported because
// leadcontrol is the lower layer and must not depend on the command package.
const leadLivenessWindow = 60 * time.Second

// LeadSessionRecord is one previous `loom lead` run, projected from its
// orchestration AgentSession down to just the fields resume reasons about.
// SessionID is loom's own id (always `lead-` prefixed); HarnessSessionID and
// CodexThreadID are the provider-side handles that actually reopen a
// conversation.
type LeadSessionRecord struct {
	SessionID     string
	AgentID       string
	Status        domain.AgentSessionStatus
	StartedAt     time.Time
	LastHeartbeat time.Time
	Finished      bool
	// FinishedAt is when the row was finalized, zero while it is unfinished.
	// Finished stays the flag resume reasons about; this is the timestamp a
	// listing shows beside it.
	FinishedAt       time.Time
	WorkDir          string
	Provider         string
	HarnessSessionID string
	CodexThreadID    string
}

// ResumeHandle returns the provider-side id that reopens this conversation on
// the given backend, or "" when the row never recorded one.
func (r LeadSessionRecord) ResumeHandle(backend string) string {
	if isCodexBackend(backend) {
		return r.CodexThreadID
	}
	return r.HarnessSessionID
}

// LeadSessionRecordFromSession projects an orchestration AgentSession. Pure:
// it reads metadata only, so the resolver below is testable without a store.
func LeadSessionRecordFromSession(session *domain.AgentSession) LeadSessionRecord {
	if session == nil {
		return LeadSessionRecord{}
	}
	m := session.Metadata
	return LeadSessionRecord{
		SessionID:        strings.TrimSpace(session.SessionID),
		AgentID:          strings.TrimSpace(session.AgentID),
		Status:           session.Status,
		StartedAt:        session.StartedAt,
		LastHeartbeat:    session.LastHeartbeat,
		Finished:         session.FinishedAt != nil,
		FinishedAt:       leadSessionFinishedAt(session),
		WorkDir:          strings.TrimSpace(m[MetadataLeadWorkDir]),
		Provider:         strings.TrimSpace(m[MetadataRuntimeProvider]),
		HarnessSessionID: strings.TrimSpace(m[MetadataHarnessSessionID]),
		CodexThreadID:    strings.TrimSpace(m[MetadataCodexThreadID]),
	}
}

// leadSessionFinishedAt dereferences the optional finish time into the zero
// value, so callers render one field instead of testing a pointer.
func leadSessionFinishedAt(session *domain.AgentSession) time.Time {
	if session == nil || session.FinishedAt == nil {
		return time.Time{}
	}
	return *session.FinishedAt
}

// LeadSessionListOptions narrows the orchestration scan.
type LeadSessionListOptions struct {
	// Limit caps the rows fetched. Zero means defaultLeadSessionListLimit.
	Limit int
}

// ListLeadSessions returns this agent's previous lead runs, newest first.
//
// The source of truth is fleet-db: `loom lead` already writes
// lead_harness_session_id / codex_provider_thread_id / lead_workdir onto its
// own orchestration session, so resume needs no new storage.
func ListLeadSessions(
	ctx context.Context,
	st store.Store,
	workspace, agentID string,
	opts LeadSessionListOptions,
) ([]LeadSessionRecord, error) {
	workspace = strings.TrimSpace(workspace)
	agentID = strings.TrimSpace(agentID)
	if st == nil || st.AgentSessions() == nil {
		return nil, fmt.Errorf("lead sessions: store unavailable")
	}
	if workspace == "" {
		return nil, fmt.Errorf("lead sessions: no active workspace")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultLeadSessionListLimit
	}
	sessions, err := st.AgentSessions().List(ctx, workspace, store.AgentSessionFilter{
		AgentID: agentID,
		Kind:    domain.AgentSessionKindOrchestration,
		Limit:   limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list lead sessions for %q: %w", agentID, err)
	}
	records := make([]LeadSessionRecord, 0, len(sessions))
	for _, s := range sessions {
		if s == nil {
			continue
		}
		records = append(records, LeadSessionRecordFromSession(s))
	}
	sortLeadSessionsNewestFirst(records)
	return records, nil
}

// sortLeadSessionsNewestFirst orders by StartedAt descending. The store's own
// ordering is not part of its contract, so resume imposes one rather than
// inheriting whichever backend answered. SessionID breaks ties so the order is
// total and the tests are not clock-sensitive.
func sortLeadSessionsNewestFirst(records []LeadSessionRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		if !records[i].StartedAt.Equal(records[j].StartedAt) {
			return records[i].StartedAt.After(records[j].StartedAt)
		}
		return records[i].SessionID > records[j].SessionID
	})
}

// ResumeRequest is the operator's ask, plus the context needed to refuse it
// safely.
type ResumeRequest struct {
	// Continue is --continue: take the most recent resumable session.
	Continue bool
	// Ref is --resume's value: a loom session id, a provider session id, or
	// ResumeLatestSentinel for a bare --resume.
	Ref string
	// Backend is the loom backend name; it decides which handle is read.
	Backend string
	// WorkDir is the directory `loom lead` was invoked from. A session
	// recorded under a different directory is refused, not silently reopened
	// somewhere else.
	WorkDir string
	// CurrentSessionID is this launch's own orchestration row, when it already
	// exists. Excluded from the candidates so a resume can never target itself.
	CurrentSessionID string
	// AgentID names the agent in refusal messages.
	AgentID string
	// Now and HeartbeatWindow drive the live-session guard. Zero values fall
	// back to time.Now and 2x leadHeartbeatInterval.
	Now             time.Time
	HeartbeatWindow time.Duration
}

// Requested reports whether either resume flag was given.
func (r ResumeRequest) Requested() bool {
	return r.Continue || strings.TrimSpace(r.Ref) != ""
}

// wantsLatest is true for --continue and for a bare --resume.
func (r ResumeRequest) wantsLatest() bool {
	ref := strings.TrimSpace(r.Ref)
	return r.Continue || ref == "" || strings.EqualFold(ref, ResumeLatestSentinel)
}

// Resume match kinds, reported so verbose output can say which id was used.
const (
	ResumeMatchLatest   = "most recent session"
	ResumeMatchLoomID   = "loom session id"
	ResumeMatchHarness  = "harness session id"
	ResumeMatchCodexTID = "codex thread id"
)

// ResumeTarget is a resolved resume: which previous row was chosen, and the
// provider-side handle to hand the backend.
type ResumeTarget struct {
	Record LeadSessionRecord
	// HarnessSessionID is the claude/harness resume id (empty for codex).
	HarnessSessionID string
	// CodexThreadID is the codex thread to reopen (empty for harness backends).
	CodexThreadID string
	// UseCodexLast asks codex for its own most recent thread because loom
	// recorded none. Always accompanied by a warning: codex's notion of "last"
	// may be a session loom never launched.
	UseCodexLast bool
	// MatchedBy is one of the ResumeMatch* constants.
	MatchedBy string
	// SkippedNoHandle counts newer sessions passed over because they recorded
	// no provider handle.
	SkippedNoHandle int
	// Warnings are non-fatal notes for the operator.
	Warnings []string
}

// ResolveResumeTarget picks the session to resume, or refuses with an error
// the operator can act on. Pure over its inputs: no store, no clock unless
// req.Now is zero.
//
// Refusing is the whole point. A silent fall back to a fresh session is the
// data-loss failure this exists to prevent, so every path here either returns
// a target or an error — never a nil target with a nil error.
func ResolveResumeTarget(records []LeadSessionRecord, req ResumeRequest) (*ResumeTarget, error) {
	if !req.Requested() {
		return nil, nil
	}
	backend := normalizeBackendName(req.Backend)
	candidates := resumeCandidates(records, req.CurrentSessionID)

	if req.wantsLatest() {
		return resolveLatestResume(candidates, req, backend)
	}
	return resolveRefResume(candidates, req, backend)
}

// resumeCandidates drops this launch's own row. Resuming it would be a session
// resuming itself, and on the codex path it would point the new runtime at the
// thread the running one is still writing.
func resumeCandidates(records []LeadSessionRecord, currentSessionID string) []LeadSessionRecord {
	current := strings.TrimSpace(currentSessionID)
	out := make([]LeadSessionRecord, 0, len(records))
	for _, r := range records {
		if current != "" && strings.EqualFold(r.SessionID, current) {
			continue
		}
		out = append(out, r)
	}
	sortLeadSessionsNewestFirst(out)
	return out
}

func resolveLatestResume(candidates []LeadSessionRecord, req ResumeRequest, backend string) (*ResumeTarget, error) {
	skipped := 0
	for _, rec := range candidates {
		if rec.ResumeHandle(backend) == "" {
			skipped++
			continue
		}
		target := &ResumeTarget{Record: rec, MatchedBy: ResumeMatchLatest, SkippedNoHandle: skipped}
		if err := finishResumeTarget(target, req, backend); err != nil {
			return nil, err
		}
		return target, nil
	}
	// Codex can reopen its own most recent thread without loom's help. Offer
	// that only for --continue (never for an explicit id) and say plainly that
	// it is codex's notion of "last", not loom's.
	if isCodexBackend(backend) && len(candidates) > 0 {
		target := &ResumeTarget{
			Record:          candidates[0],
			MatchedBy:       ResumeMatchLatest,
			UseCodexLast:    true,
			SkippedNoHandle: skipped,
		}
		target.Warnings = append(target.Warnings,
			"no codex thread id recorded for the previous lead sessions; falling back to 'codex resume --last', "+
				"which is codex's own most recent thread and may be a session loom did not launch")
		if err := finishResumeTarget(target, req, backend); err != nil {
			return nil, err
		}
		return target, nil
	}
	return nil, newNoResumableSessionError(req, skipped)
}

func newNoResumableSessionError(req ResumeRequest, skipped int) error {
	agent := strings.TrimSpace(req.AgentID)
	base := fmt.Errorf("no previous lead session for agent %q in %s; run 'loom lead' to start one",
		agent, req.WorkDir)
	if skipped > 0 {
		return fmt.Errorf("%w (skipped %d session(s) with no recorded resume id)", base, skipped)
	}
	return base
}

func resolveRefResume(candidates []LeadSessionRecord, req ResumeRequest, backend string) (*ResumeTarget, error) {
	ref := strings.TrimSpace(req.Ref)
	// Loom ids are `lead-` prefixed and provider ids are bare uuids, so the
	// two spaces cannot collide — but match loom's own id first anyway, so the
	// answer never depends on that invariant holding.
	for _, rec := range candidates {
		if strings.EqualFold(rec.SessionID, ref) {
			target := &ResumeTarget{Record: rec, MatchedBy: ResumeMatchLoomID}
			if rec.ResumeHandle(backend) == "" {
				return nil, fmt.Errorf(
					"lead session %s has no recorded %s resume id; it was never launched under a controlled %s runtime",
					rec.SessionID, backend, backend)
			}
			if err := finishResumeTarget(target, req, backend); err != nil {
				return nil, err
			}
			return target, nil
		}
	}
	for _, rec := range candidates {
		if rec.ResumeHandle(backend) != "" && strings.EqualFold(rec.ResumeHandle(backend), ref) {
			match := ResumeMatchHarness
			if isCodexBackend(backend) {
				match = ResumeMatchCodexTID
			}
			target := &ResumeTarget{Record: rec, MatchedBy: match}
			if err := finishResumeTarget(target, req, backend); err != nil {
				return nil, err
			}
			return target, nil
		}
	}
	return nil, fmt.Errorf("no lead session matching %q for agent %q; run 'loom lead' to start one",
		ref, strings.TrimSpace(req.AgentID))
}

// finishResumeTarget applies the guards that can still refuse a matched row
// and fills in the provider handle.
func finishResumeTarget(target *ResumeTarget, req ResumeRequest, backend string) error {
	if err := checkResumeWorkDir(target.Record, req); err != nil {
		return err
	}
	if err := checkResumeNotLive(target.Record, req); err != nil {
		return err
	}
	if isCodexBackend(backend) {
		target.CodexThreadID = target.Record.CodexThreadID
	} else {
		target.HarnessSessionID = target.Record.HarnessSessionID
	}
	return nil
}

// checkResumeWorkDir refuses a session recorded under a different directory.
// The harness resolves its transcript by working directory (claude names its
// project dir after the cwd), so resuming from elsewhere either finds nothing
// or attaches a conversation to the wrong repo.
func checkResumeWorkDir(rec LeadSessionRecord, req ResumeRequest) error {
	want := strings.TrimSpace(req.WorkDir)
	got := strings.TrimSpace(rec.WorkDir)
	if want == "" || got == "" || want == got {
		return nil
	}
	return fmt.Errorf(
		"lead session %s was started in %s but this shell is in %s; cd there and retry (resume does not relocate a conversation)",
		rec.SessionID, got, want)
}

// checkResumeNotLive is a best-effort guard against two processes writing one
// transcript. It is best-effort by construction: the heartbeat is up to one
// interval stale and a force-killed lead never clears its status, so the
// window is deliberately generous rather than exact.
func checkResumeNotLive(rec LeadSessionRecord, req ResumeRequest) error {
	if rec.Finished || rec.Status != domain.AgentSessionRunning {
		return nil
	}
	window := req.HeartbeatWindow
	if window <= 0 {
		window = leadLivenessWindow
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	if rec.LastHeartbeat.IsZero() || now.Sub(rec.LastHeartbeat) > window {
		return nil
	}
	return fmt.Errorf(
		"lead session %s is still running (heartbeat %s ago); resuming it would have two processes writing one transcript. "+
			"Exit that session first, or resume a different one",
		rec.SessionID, now.Sub(rec.LastHeartbeat).Truncate(time.Second))
}

func isCodexBackend(backend string) bool {
	return normalizeBackendName(backend) == RuntimeProviderCodex
}

func normalizeBackendName(backend string) string {
	return strings.ToLower(strings.TrimSpace(backend))
}
