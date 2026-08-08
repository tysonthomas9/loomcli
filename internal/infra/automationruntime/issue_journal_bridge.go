package trigger

// IssueJournalBridge is the A4 loopback bridge: it polls fleet-db's issue
// mutation journal and re-enters each issue lifecycle entry into the trigger
// router through InternalSource.Emit, stamped origin=system. It is the
// system-origin sibling of the workflow-origin emit lane (the driver-op
// "emit-event" API): both land on the same loopback ingress, both carry
// structural provenance, neither one filters actors.
//
// DETERMINISTIC EVENT IDS / IDEMPOTENT REPLAY. Every journal entry becomes an
// InternalEvent whose EventID is "fleet-journal-{streamID}". Because the
// loopback idempotency key is internal:{ws}:fleet-journal-{streamID}
// (InternalEventIdempotencyKey, internal_source.go), every re-emission of the
// same journal entry dedups exactly-once in the dispatch path — the
// TriggerEvent and every fan-out leg collapse onto the stored record. The
// cursor is therefore an OPTIMIZATION, not a correctness boundary: losing it
// (serve restart, replay opt-in) re-emits already-seen entries that the
// dispatch path silently dedups. We still advance it so the steady state polls
// only the journal tail.
//
// FIRST-RUN FAST-FORWARD. The very first poll of a workspace with no stored
// cursor does NOT emit historical issues — it fast-forwards the cursor to the
// journal tail. A platform that has accumulated issues before the bridge was
// switched on must not trigger a triage storm over its entire backlog the
// instant the bridge starts. Set LOOM_ISSUE_BRIDGE_REPLAY=1 to opt into
// replay-from-zero instead (safe precisely because dedup absorbs the re-runs).
//
// SELF-TRIGGER STORY (mandatory — read before binding anything to
// internal.issue.created). The bridge stamps origin=system and NEVER FILTERS
// ACTORS: an issue created by a workflow run is journaled with that run's actor
// and the bridge faithfully re-emits it. So a binding on internal.issue.created
// whose driver itself creates issues will re-trigger off its own output. The
// structural hop-depth cap (automation.DefaultInternalEventHopDepthCap=4) is the backstop
// for CHAINS, but it does NOT stop a depth-0 re-trigger: a system root sits at
// hop_depth=0 (internal_source.go guardProvenance) and never trips the cap, so
// a self-feeding binding loops at depth 0 forever, capped only by issue-create
// throughput. The BINDING re-trigger guard is therefore the actor filter
// (automation.ActorFilter): every binding on internal.issue.created MUST
// carry exclude_actor_kinds (exclude task-run:* / workflow actors) so it reacts
// to human/external issue creation but not to issues its own runs produced. The
// A4-4 setup script encodes exactly that filter; the bridge deliberately does
// NOT filter here, because actor scoping is a per-binding policy, not a
// transport concern, and a different binding may legitimately want the
// workflow-authored issues.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// IssueJournalEventIDPrefix anchors the deterministic loopback event id derived
// from a journal stream position: "fleet-journal-{streamID}". Exported so the
// A4-4 setup/audit tooling can reconstruct the idempotency key a journal entry
// re-entered under.
const IssueJournalEventIDPrefix = "fleet-journal-"

// IssueJournalSubjectRefPrefix scopes the loopback subject ref to the issue
// namespace: "issue:{entityID}".
const IssueJournalSubjectRefPrefix = "issue:"

// DefaultIssueJournalBatchLimit bounds ListIssueEvents per workspace per pass.
const DefaultIssueJournalBatchLimit = 100

// issueJournalReplayEnv opts into replay-from-zero on first observation of a
// workspace (no stored cursor): when set to "1"/"true" the bootstrap pass
// emits the whole journal from the beginning instead of fast-forwarding to the
// tail. Dedup makes the replay safe.
const issueJournalReplayEnv = "LOOM_ISSUE_BRIDGE_REPLAY"

// issueJournalMaxBackoffShift caps the consecutive-failure backoff exponent so
// the skip window cannot grow without bound; mirrors EventHub's
// maxConsecutiveFailures discipline. With this shift a workspace whose reader
// keeps failing is retried at most every 2^shift sweeps.
const issueJournalMaxBackoffShift = 6

// defaultIssueJournalActionAllowlist is the default set of journal actions the
// bridge re-emits. v1 ships issue.create only; close/reopen sit behind the
// allowlist for a later roster expansion so a single binding cannot
// accidentally react to the full issue lifecycle before the downstream agents
// are ready for it.
var defaultIssueJournalActionAllowlist = []string{"issue.create"}

// IssueJournalCursorStore persists the per-workspace journal cursor across
// bridge passes. The in-memory implementation (issueJournalMemCursors, the zero
// value of the bridge's own store) is sufficient today: cursor loss only costs
// a dedup-absorbed replay, never correctness. A durable implementation can be
// injected later without touching the bridge core.
type IssueJournalCursorStore interface {
	// Load returns the stored cursor for the workspace and whether one exists.
	// found=false signals the first observation of the workspace (bootstrap
	// fast-forward / replay decision).
	Load(ws string) (cursor string, found bool)
	// Save records the workspace's resume cursor.
	Save(ws, cursor string)
}

// IssueJournalBridge polls the issue journal and re-emits each allowed entry
// into the trigger router as a system-origin internal event. RunOnce is shaped
// like DeliverySweeper.RunOnce: per workspace, load the cursor, page the
// reader, emit allowed events, advance the cursor only past durably-handled
// entries. The zero value plus Store, Source and Reader is ready to use and is
// safe for concurrent RunOnce calls.
type IssueJournalBridge struct {
	// Store resolves the sweep's workspace set (mirrors DeliverySweeper).
	Store workspaceLister
	// Source is the loopback ingress every journal entry re-enters through.
	// Source is the single system-event admission seam. Production serve
	// supplies an Automation-backed emitter; InternalSource is retained only as
	// the legacy conformance implementation used by isolated trigger tests.
	Source InternalEventEmitter
	// Reader is the issue-journal read capability (store.IssueJournalReader,
	// the fleet-db-only capability from A4-1). Required.
	Reader store.IssueJournalReader
	// ActionAllowlist names the journal actions to re-emit (e.g.
	// "issue.create"). Empty falls back to defaultIssueJournalActionAllowlist.
	ActionAllowlist []string
	// BatchLimit bounds ListIssueEvents per workspace per pass; zero or
	// negative falls back to DefaultIssueJournalBatchLimit.
	BatchLimit int
	// EmitTaskReady turns on the task-ready lane: in addition to the
	// normal allowlisted re-emission, an entry that marks a task becoming ready
	// (issue.create / issue.update to an open status) also emits a task.ready
	// internal event carrying the task id, so a prompt-agent binding on
	// internal.task.ready can claim THAT task. The field itself defaults false
	// for explicit construction in tests and alternate hosts; loom serve enables
	// it unconditionally as a generic platform event lane.
	// See issue_journal_bridge_task_ready.go.
	EmitTaskReady bool
	// EmitTaskReview turns on the task-review transition lane: a proven
	// issue.update transition from a non-review status into review emits a
	// task.review internal event. The field itself defaults false for explicit
	// construction; loom serve enables it unconditionally. See
	// issue_journal_bridge_task_review.go.
	EmitTaskReview bool
	// IssueLookup resolves the CURRENT design/labels/type of an issue for
	// task.ready/task.review payload enrichment. It is needed because an issue.update
	// journal entry's After snapshot is a DELTA (only the changed fields): an
	// absent design key there means UNKNOWN, not "no design" — emitting a
	// false hasDesign mis-routes the planner/coder phase gate both ways (the
	// 2026-07-07 approve-transition bug). Nil disables lookup; the payload then
	// OMITS unknowable keys so the claim gate falls back to claim-then-check.
	IssueLookup TaskReadyIssueLookup
	// ReadySnapshots lists the CURRENT canonical Ready view for startup
	// reconciliation. Unlike replaying the journal from zero, this admits only
	// tasks that are ready now, so enabling task-ready events after the shared
	// journal cursor has advanced cannot strand an existing task or create a
	// historical triage storm. Nil preserves the journal-only compatibility path.
	ReadySnapshots TaskReadySnapshotLister
	// RepositoryRequiredBlocker is the Work Items-owned commit-time admission
	// command used for every non-epic task without an explicit repository. The
	// pre-read RepositoryRequired flag is not authority because a single-repo
	// fallback can race deletion of that sole Repo. The bridge never writes an
	// issue directly: a successful block suppresses task.ready delivery; when
	// the command instead observes a commit-time ready task, its canonical
	// projection replaces the stale event payload and dispatch continues. Nil
	// preserves the event-only compatibility path used by alternate hosts.
	RepositoryRequiredBlocker TaskReadyRepositoryRequiredBlocker
	// WorkspaceKey scopes the sweep to one workspace. Empty sweeps every known
	// workspace (mirrors DeliverySweeper/CronScheduler).
	WorkspaceKey string
	// Cursors persists the per-workspace resume cursor; nil uses the bridge's
	// in-memory store.
	Cursors IssueJournalCursorStore
	// Logger receives skip/backoff audit records (slog.Default when nil).
	Logger *slog.Logger

	mu sync.Mutex
	// memCursors backs the in-memory cursor store when Cursors is nil.
	memCursors map[string]string
	// failures counts consecutive workspace-pass failures (snapshot, reader or
	// admission), driving exponential skip backoff; reset after a clean pass.
	failures map[string]int
	// skipRemaining counts how many upcoming sweeps a workspace is still paused
	// for by the failure backoff; decremented each pass, replenished on a fresh
	// failure (clock-free window — no wall clock needed in serve or tests).
	skipRemaining map[string]int
	// taskReadyReconciled records workspaces whose current Ready view completed
	// its once-per-process reconciliation. A failed/partial pass is retried; the
	// synthetic event IDs make every replay idempotent.
	taskReadyReconciled map[string]bool
	// taskReadyGenerations remembers the UpdatedAt generation emitted by the
	// reconciliation lane. Journal catch-up suppresses an equal/older natural
	// task.ready occurrence so startup cannot admit two runs for the same ready
	// generation.
	taskReadyGenerations map[string]map[string]taskReadyGeneration
}

// IssueJournalSweepResult summarizes one RunOnce pass across every swept
// workspace.
type IssueJournalSweepResult struct {
	// Emitted counts journal entries re-entered into the router (admitted or
	// dispatch-deduped).
	Emitted int
	// Skipped counts entries passed over by the action allowlist.
	Skipped int
	// TaskReadyEmitted counts task.ready events emitted by the flag-gated
	// task-ready lane (EmitTaskReady); zero when the flag is off.
	TaskReadyEmitted int
	// TaskReviewEmitted counts task.review events emitted by the flag-gated
	// review-transition lane (EmitTaskReview); zero when the flag is off.
	TaskReviewEmitted int
	// TaskReadyBlocked counts current repository-required tasks moved to the
	// Work Items blocked state instead of being emitted to Automation.
	TaskReadyBlocked int
	// FastForwarded counts workspaces bootstrapped to the journal tail without
	// emitting (first observation, replay disabled).
	FastForwarded int
	// BackedOff counts workspaces skipped this pass by the consecutive-failure
	// backoff window.
	BackedOff int
}

// RunOnce performs a single bridge pass over every target workspace. It keeps
// going past per-workspace errors and returns them joined; a non-nil result is
// returned even when some workspaces errored.
func (b *IssueJournalBridge) RunOnce(ctx context.Context) (*IssueJournalSweepResult, error) {
	if b == nil || b.Store == nil || b.Source == nil || b.Reader == nil {
		return nil, fmt.Errorf("issue journal bridge: store, source and reader are required: %w", domain.ErrInvalid)
	}
	workspaces, err := b.workspaceKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := &IssueJournalSweepResult{}
	var errs []error
	for _, ws := range workspaces {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if err := b.sweepWorkspace(ctx, ws, out); err != nil {
			errs = append(errs, err)
		}
	}
	return out, errors.Join(errs...)
}

// sweepWorkspace runs one workspace's bridge pass: honor the failure backoff,
// resolve the resume cursor (bootstrapping to the tail on first observation
// unless replay is opted in), page the reader, and emit allowed entries,
// advancing the cursor only past durably-handled ones.
func (b *IssueJournalBridge) sweepWorkspace(ctx context.Context, ws string, out *IssueJournalSweepResult) error {
	if b.inBackoffWindow(ws) {
		out.BackedOff++
		return nil
	}
	if err := b.reconcileTaskReadyOnce(ctx, ws, out); err != nil {
		b.recordFailure(ws)
		return err
	}
	cursor, found := b.loadCursor(ws)
	var err error
	if !found {
		err = b.bootstrap(ctx, ws, out)
	} else {
		err = b.drainFrom(ctx, ws, cursor, out)
	}
	if err != nil {
		b.recordFailure(ws)
		return err
	}
	// Clear the failure window only after the entire workspace pass is clean.
	// Reconciliation, journal reads and event admission are one health unit: a
	// persistent failure in any lane must not recreate a two-second log loop.
	b.recordSuccess(ws)
	return nil
}

// bootstrap handles the first observation of a workspace. Replay opt-in drains
// from the beginning (dedup-absorbed); otherwise it fast-forwards the cursor to
// the current journal tail WITHOUT emitting, so the bridge never triages the
// historical backlog.
func (b *IssueJournalBridge) bootstrap(ctx context.Context, ws string, out *IssueJournalSweepResult) error {
	if replayFromZeroEnabled() {
		return b.drainFrom(ctx, ws, "", out)
	}
	tail, err := b.journalTail(ctx, ws)
	if err != nil {
		return err
	}
	b.saveCursor(ws, tail)
	out.FastForwarded++
	return nil
}

// journalTail pages the reader from the beginning purely to learn the latest
// stream position, emitting nothing. The last batch's nextCursor is the tail.
func (b *IssueJournalBridge) journalTail(ctx context.Context, ws string) (string, error) {
	cursor := ""
	for {
		_, next, hasMore, err := b.Reader.ListIssueEvents(ctx, ws, cursor, b.batchLimit())
		if err != nil {
			return "", fmt.Errorf("fast-forward issue journal in workspace %q: %w", ws, err)
		}
		cursor = next
		if !hasMore {
			return cursor, nil
		}
		if ctx.Err() != nil {
			return cursor, ctx.Err()
		}
	}
}

// drainFrom pages the journal from the cursor, emitting allowed entries until
// the reader reaches the journal tail. On a fully-handled page the cursor
// advances to the reader's own resume position (nextCursor) — not merely the
// last EMITTED entry — so a page that filtered out ENTIRELY still makes forward
// progress. On a mid-batch Emit failure the cursor advances only per-event (past
// durably-handled entries), so the failed entry is retried exactly there next
// pass without re-emitting handled entries or skipping unhandled ones.
//
// WHY nextCursor, NOT the last emitted id. ListIssueEvents filters to
// entity_type=issue at the reader/fleet-db boundary, so a page can return ZERO
// issue events while its raw window held a run of non-issue mutations
// (driver_run/task_run/role churn dominates a busy stream). fleet-db reports
// has_more against the POST-FILTER count, so such a page arrives as
// {events:[], nextCursor:<advanced>, hasMore:false}. Advancing only by the last
// emitted id (empty here) and stopping on !hasMore would pin the cursor forever,
// re-reading the same all-filtered page and never reaching later issue events.
// Advancing to nextCursor and continuing while the cursor still moves keeps the
// bridge draining past non-issue runs; the loop terminates at the tail, where
// the reader echoes the asked-for cursor (no forward movement).
func (b *IssueJournalBridge) drainFrom(ctx context.Context, ws, cursor string, out *IssueJournalSweepResult) error {
	for {
		events, nextCursor, hasMore, err := b.Reader.ListIssueEvents(ctx, ws, cursor, b.batchLimit())
		if err != nil {
			return fmt.Errorf("poll issue journal in workspace %q: %w", ws, err)
		}
		emitted, perr := b.emitBatch(ctx, ws, events, out)
		if perr != nil {
			// Mid-batch Emit failure: advance only past durably-handled entries so
			// the failed entry is retried next pass — never skip ahead to nextCursor.
			if emitted != "" {
				cursor = emitted
				b.saveCursor(ws, cursor)
			}
			return perr
		}
		// Whole page handled: jump to the reader's resume position so an
		// all-filtered page still advances (fall back to the last emitted id if the
		// reader reported no cursor).
		prev := cursor
		if nextCursor != "" {
			cursor = nextCursor
		} else if emitted != "" {
			cursor = emitted
		}
		if cursor != prev {
			b.saveCursor(ws, cursor)
		}
		// Stop only at the tail: nothing more AND the cursor stopped moving. A
		// forward-moving cursor keeps draining even when a filtered page reports
		// has_more=false (that flag counts post-filter matches, not raw tail depth).
		if !hasMore && cursor == prev {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

// emitBatch processes one page oldest-first. It returns the cursor to persist
// (the stream id of the last durably-handled event, "" when none advanced) and
// the first non-terminal Emit error, which stops the drain so the unhandled
// entry is retried next pass. Each entry may emit up to three events: the normal
// allowlisted re-emission and, when the flag-gated task-ready lane is on, an
// additional task.ready event, plus a task.review event when its independently
// gated transition lane is on; the cursor advances only after every applicable
// event is durably handled, so a failure resumes exactly at this entry.
func (b *IssueJournalBridge) emitBatch(ctx context.Context, ws string, events []store.JournalEvent, out *IssueJournalSweepResult) (string, error) {
	advanced := ""
	for _, ev := range events {
		if ctx.Err() != nil {
			return advanced, ctx.Err()
		}
		if err := b.emitJournalEntry(ctx, ws, ev, out); err != nil {
			return advanced, err
		}
		advanced = ev.ID
	}
	return advanced, nil
}

func (b *IssueJournalBridge) emitJournalEntry(
	ctx context.Context,
	ws string,
	ev store.JournalEvent,
	out *IssueJournalSweepResult,
) error {
	if b.actionAllowed(ev.Action) {
		if err := b.emitOne(ctx, ws, ev); err != nil {
			return err
		}
		out.Emitted++
	} else {
		out.Skipped++
	}
	if b.EmitTaskReady && isTaskReadyEntry(ev) {
		reconciled, err := b.taskReadyJournalGenerationReconciled(ctx, ws, ev)
		if err != nil {
			return err
		}
		if reconciled {
			return nil
		}
		emitted, blocked, err := b.emitTaskReady(ctx, ws, ev)
		if err != nil {
			return err
		}
		if emitted {
			out.TaskReadyEmitted++
		}
		if blocked {
			out.TaskReadyBlocked++
		}
	}
	if b.EmitTaskReview && isTaskReviewEntry(ev) {
		emitted, err := b.emitTaskReview(ctx, ws, ev)
		if err != nil {
			return err
		}
		if emitted {
			out.TaskReviewEmitted++
		}
	}
	return nil
}

// emitOne re-enters one journal entry into the router as a system-origin
// internal event. A no-listener dispatch (domain.ErrNotFound — no binding on
// internal.issue.created) is NOT a bridge failure: the cursor still advances so
// a missing binding never permanently stalls the bridge. Any other Emit error
// is returned so the drain stops and the entry is retried.
func (b *IssueJournalBridge) emitOne(ctx context.Context, ws string, ev store.JournalEvent) error {
	_, err := b.Source.Emit(ctx, ws, b.toInternalEvent(ev))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		b.logger().Debug("issue journal bridge: no binding for internal event, advancing past it",
			"workspace", ws, "event_id", IssueJournalEventIDPrefix+ev.ID, "action", ev.Action)
		return nil
	default:
		return fmt.Errorf("emit issue journal event %q in workspace %q: %w", ev.ID, ws, err)
	}
}

// toInternalEvent maps a journal entry to the loopback InternalEvent. EventID
// is deterministic (fleet-journal-{streamID}) so replay dedups; Origin is
// system; ParentEventID is empty (a depth-0 root — the journal entry is not a
// continuation of a known trigger chain); ActorRef is the journal actor
// VERBATIM (the bridge never filters or rewrites it — see the self-trigger
// story); SubjectAttrs are extracted from the After snapshot.
func (b *IssueJournalBridge) toInternalEvent(ev store.JournalEvent) InternalEvent {
	return InternalEvent{
		EventID:      IssueJournalEventIDPrefix + ev.ID,
		EventType:    ev.Action,
		Origin:       automation.EventOriginSystem,
		ActorRef:     ev.Actor,
		SubjectRef:   IssueJournalSubjectRefPrefix + ev.EntityID,
		Payload:      ev.After,
		SubjectAttrs: issueSubjectAttrs(ev.After),
	}
}

// workspaceKeys resolves the sweep targets: the configured workspace, or every
// known workspace when unscoped (mirrors DeliverySweeper).
func (b *IssueJournalBridge) workspaceKeys(ctx context.Context) ([]string, error) {
	if b.WorkspaceKey != "" {
		return []string{b.WorkspaceKey}, nil
	}
	workspaces, err := b.Store.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces for issue journal sweep: %w", err)
	}
	keys := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}
		keys = append(keys, ws.Key)
	}
	return keys, nil
}

func (b *IssueJournalBridge) batchLimit() int {
	if b.BatchLimit > 0 {
		return b.BatchLimit
	}
	return DefaultIssueJournalBatchLimit
}

// actionAllowed reports whether the journal action is in the (case-insensitive,
// trimmed) allowlist. Empty allowlist falls back to the v1 default.
func (b *IssueJournalBridge) actionAllowed(action string) bool {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return false
	}
	allow := b.ActionAllowlist
	if len(allow) == 0 {
		allow = defaultIssueJournalActionAllowlist
	}
	for _, a := range allow {
		if strings.ToLower(strings.TrimSpace(a)) == action {
			return true
		}
	}
	return false
}

func (b *IssueJournalBridge) logger() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}

// loadCursor reads the workspace's resume cursor through the configured store
// (in-memory fallback when nil).
func (b *IssueJournalBridge) loadCursor(ws string) (string, bool) {
	if b.Cursors != nil {
		return b.Cursors.Load(ws)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	cursor, ok := b.memCursors[ws]
	return cursor, ok
}

func (b *IssueJournalBridge) saveCursor(ws, cursor string) {
	if b.Cursors != nil {
		b.Cursors.Save(ws, cursor)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.memCursors == nil {
		b.memCursors = make(map[string]string)
	}
	b.memCursors[ws] = cursor
}

// inBackoffWindow reports whether this pass should skip the workspace because
// of recent consecutive pass failures, decrementing the paused-sweep
// countdown. The window doubles per consecutive failure (recordFailure sets
// 2^min(f,cap)-1 upcoming sweeps to skip), so a flapping reader is retried with
// exponential backoff measured in sweeps — clock-free, like CronScheduler's
// window. A fully clean workspace pass clears it.
func (b *IssueJournalBridge) inBackoffWindow(ws string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.skipRemaining[ws]
	if remaining <= 0 {
		return false
	}
	b.skipRemaining[ws] = remaining - 1
	b.logger().Debug("issue journal bridge: workspace in failure backoff",
		"workspace", ws, "consecutive_failures", b.failures[ws], "skip_remaining", remaining)
	return true
}

// recordFailure bumps the workspace's consecutive-failure count and suspends the
// next 2^min(failures,cap)-1 sweeps (the doubled backoff window): one failure
// skips the next sweep, two failures the next three, and so on up to the cap.
func (b *IssueJournalBridge) recordFailure(ws string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures == nil {
		b.failures = make(map[string]int)
	}
	b.failures[ws]++
	shift := b.failures[ws]
	if shift > issueJournalMaxBackoffShift {
		shift = issueJournalMaxBackoffShift
	}
	if b.skipRemaining == nil {
		b.skipRemaining = make(map[string]int)
	}
	b.skipRemaining[ws] = (1 << shift) - 1
}

// recordSuccess clears the workspace's failure backoff after a clean pass.
func (b *IssueJournalBridge) recordSuccess(ws string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.failures, ws)
	delete(b.skipRemaining, ws)
}

// issueSubjectAttrKeys are the issue-snapshot fields the bridge lifts into the
// adapter-enriched SubjectAttrs lane (the first internal source to populate it,
// so subject-key templates can read {{attrs.status}} etc. without touching the
// raw payload). fleet-db issue snapshots are snake_case v1 wire.
var issueSubjectAttrKeys = []string{"status", "title", "repo", "created_by"}

// issueSubjectAttrs extracts the templated subject attributes from a journal
// entry's After snapshot: status, title, repo and created_by, each rendered to
// a string (a non-string scalar — e.g. a numeric issue number under one of
// these keys — is stringified; objects/arrays/nulls and absent keys are
// dropped). A nil/empty/non-object After yields a nil map so the dispatch path
// falls back to the default subject key.
func issueSubjectAttrs(after json.RawMessage) map[string]string {
	if len(after) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(after, &fields); err != nil {
		return nil
	}
	attrs := make(map[string]string, len(issueSubjectAttrKeys))
	for _, key := range issueSubjectAttrKeys {
		if raw, ok := fields[key]; ok {
			if v, ok := scalarString(raw); ok {
				attrs[key] = v
			}
		}
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

// scalarString renders a JSON scalar (string, number, bool) to its string
// form; objects, arrays and null report ok=false so the attr is dropped rather
// than carrying a brace-laden blob into a subject key.
func scalarString(raw json.RawMessage) (string, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", false
	}
	switch trimmed[0] {
	case '{', '[':
		return "", false
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", false
		}
		return s, s != ""
	case 't', 'f':
		return trimmed, true
	default:
		// Number: round-trip through json.Number so 42 stays "42", not "42.0".
		var n json.Number
		if err := json.Unmarshal(raw, &n); err != nil {
			return "", false
		}
		return numberString(n), true
	}
}

// numberString normalizes a JSON number to a clean string (integers without a
// trailing ".0").
func numberString(n json.Number) string {
	if i, err := n.Int64(); err == nil {
		return strconv.FormatInt(i, 10)
	}
	return n.String()
}

// replayFromZeroEnabled reports whether the bootstrap pass should replay the
// whole journal from the beginning rather than fast-forwarding to the tail.
func replayFromZeroEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(issueJournalReplayEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
