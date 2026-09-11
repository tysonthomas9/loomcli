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
// structural hop-depth cap (DefaultInternalEventHopDepthCap=4) is the backstop
// for CHAINS, but it does NOT stop a depth-0 re-trigger: a system root sits at
// hop_depth=0 (internal_source.go guardProvenance) and never trips the cap, so
// a self-feeding binding loops at depth 0 forever, capped only by issue-create
// throughput. The BINDING re-trigger guard is therefore the actor filter
// (domain.TriggerActorFilter): every binding on internal.issue.created MUST
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
//
// The default is deliberately unchanged: serve widens it per-deployment through
// LOOM_ISSUE_BRIDGE_ACTIONS (see serve.issueBridgeActions for why the roster is
// opt-in rather than default-on).
var defaultIssueJournalActionAllowlist = []string{"issue.create"}

// IssueJournalActions resolves an ActionAllowlist to the roster the bridge will
// actually apply: the configured list, or the default when it is empty. Exported
// so callers (serve's startup log, audit tooling) can report the EFFECTIVE
// roster instead of re-deriving the nil-means-default rule and drifting from it.
// The returned slice is a copy and is safe to retain.
func IssueJournalActions(allowlist []string) []string {
	if len(allowlist) == 0 {
		return append([]string(nil), defaultIssueJournalActionAllowlist...)
	}
	return append([]string(nil), allowlist...)
}

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
	Store store.Store
	// Source is the loopback ingress every journal entry re-enters through.
	Source *InternalSource
	// Reader is the issue-journal read capability (store.IssueJournalReader,
	// the fleet-db-only capability from A4-1). Required.
	Reader store.IssueJournalReader
	// ActionAllowlist names the journal actions to re-emit (e.g.
	// "issue.create"). Empty falls back to defaultIssueJournalActionAllowlist.
	ActionAllowlist []string
	// BatchLimit bounds ListIssueEvents per workspace per pass; zero or
	// negative falls back to DefaultIssueJournalBatchLimit.
	BatchLimit int
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
	// failures counts consecutive reader failures per workspace, driving the
	// exponential skip backoff; reset on the first clean poll.
	failures map[string]int
	// skipRemaining counts how many upcoming sweeps a workspace is still paused
	// for by the failure backoff; decremented each pass, replenished on a fresh
	// failure (clock-free window — no wall clock needed in serve or tests).
	skipRemaining map[string]int
}

// IssueJournalSweepResult summarizes one RunOnce pass across every swept
// workspace.
type IssueJournalSweepResult struct {
	// Emitted counts journal entries re-entered into the router (admitted or
	// dispatch-deduped).
	Emitted int
	// Skipped counts entries passed over by the action allowlist.
	Skipped int
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
	cursor, found := b.loadCursor(ws)
	if !found {
		return b.bootstrap(ctx, ws, out)
	}
	return b.drainFrom(ctx, ws, cursor, out)
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
		b.recordFailure(ws)
		return err
	}
	b.recordSuccess(ws)
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
// the reader reports no more. The cursor advances per-event only after Emit
// durably handled the entry (success or dispatch-dedup), so a mid-batch Emit
// failure resumes exactly there on the next pass without re-emitting handled
// entries or skipping unhandled ones.
func (b *IssueJournalBridge) drainFrom(ctx context.Context, ws, cursor string, out *IssueJournalSweepResult) error {
	for {
		events, _, hasMore, err := b.Reader.ListIssueEvents(ctx, ws, cursor, b.batchLimit())
		if err != nil {
			b.recordFailure(ws)
			return fmt.Errorf("poll issue journal in workspace %q: %w", ws, err)
		}
		b.recordSuccess(ws)
		next, perr := b.emitBatch(ctx, ws, events, out)
		if next != "" {
			cursor = next
			b.saveCursor(ws, cursor)
		}
		if perr != nil {
			return perr
		}
		if !hasMore {
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
// entry is retried next pass.
func (b *IssueJournalBridge) emitBatch(ctx context.Context, ws string, events []store.JournalEvent, out *IssueJournalSweepResult) (string, error) {
	advanced := ""
	for _, ev := range events {
		if ctx.Err() != nil {
			return advanced, ctx.Err()
		}
		if !b.actionAllowed(ev.Action) {
			out.Skipped++
			advanced = ev.ID
			continue
		}
		if err := b.emitEvent(ctx, ws, b.toInternalEvent(ev)); err != nil {
			return advanced, err
		}
		out.Emitted++
		advanced = ev.ID
	}
	return advanced, nil
}

// emitEvent re-enters one derived or pass-through internal event into the
// router. A no-listener dispatch (domain.ErrNotFound — no binding on
// internal.issue.created) is NOT a bridge failure: the cursor still advances so
// a missing binding never permanently stalls the bridge. Any other Emit error
// is returned so the drain stops and the entry is retried.
func (b *IssueJournalBridge) emitEvent(ctx context.Context, ws string, ev InternalEvent) error {
	_, err := b.Source.Emit(ctx, ws, ev)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		b.logger().Debug("issue journal bridge: no binding for internal event, advancing past it",
			"workspace", ws, "event_id", ev.EventID, "event_type", ev.EventType)
		return nil
	default:
		return fmt.Errorf("emit issue journal event %q in workspace %q: %w", ev.EventID, ws, err)
	}
}

// Label journal actions. fleet-db emits a DEDICATED event per label write
// (issue_service.AddLabel/RemoveLabel) rather than folding it into a
// whole-entity issue.update: action label.add / label.remove, entity_type
// "label", entity_id the ISSUE id, and the label itself in the event metadata.
// Neither verb is in internalEventVerbNormalization, so both pass through
// unchanged and route as internal.label.add / internal.label.remove.
const (
	IssueLabelAddAction    = "label.add"
	IssueLabelRemoveAction = "label.remove"
)

// issueJournalLabelMetaKey is fleet-db's metadata key carrying the single label
// a label.add/label.remove event is about (models.MetaLabel).
const issueJournalLabelMetaKey = "label"

// IssueLabelSubjectAttr is the scalar subject attr carrying that label,
// addressable as {{attrs.label}} in a binding's subject_key_template. The
// snapshot's own labels field is an ARRAY, which issueSubjectAttrs drops, so
// lifting the metadata value is what makes a label nameable in a template.
const IssueLabelSubjectAttr = "label"

// isIssueLabelAction reports whether the journal action is a per-label write.
func isIssueLabelAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case IssueLabelAddAction, IssueLabelRemoveAction:
		return true
	default:
		return false
	}
}

// toInternalEvent maps a journal entry to the loopback InternalEvent. EventID
// is deterministic (fleet-journal-{streamID}) so replay dedups; Origin is
// system; ParentEventID is empty (a depth-0 root — the journal entry is not a
// continuation of a known trigger chain); ActorRef is the journal actor
// VERBATIM (the bridge never filters or rewrites it — see the self-trigger
// story); SubjectAttrs are extracted from the After snapshot, plus the label
// metadata on a label.add/label.remove entry.
//
// SubjectRef is the ISSUE ref on every entry including the label ones: their
// entity_type is "label" but their entity_id is the issue id, so the whole
// journal addresses one subject namespace and an await keyed on an issue
// matches whichever lifecycle event names it.
func (b *IssueJournalBridge) toInternalEvent(ev store.JournalEvent) InternalEvent {
	return InternalEvent{
		EventID:      IssueJournalEventIDPrefix + ev.ID,
		EventType:    ev.Action,
		Origin:       domain.TriggerEventOriginSystem,
		ActorRef:     ev.Actor,
		SubjectRef:   IssueJournalSubjectRefPrefix + ev.EntityID,
		Payload:      ev.After,
		SubjectAttrs: issueEventSubjectAttrs(ev),
	}
}

// issueEventSubjectAttrs builds one entry's subject attrs: the After-snapshot
// projection, plus {{attrs.label}} lifted from the metadata on a per-label
// entry. The metadata is the ONLY place the individual label appears as a
// scalar — the snapshot carries the whole labels array, which issueSubjectAttrs
// drops. An entry whose metadata is missing the key contributes no label attr
// rather than an empty one, so a template naming it falls back to the default
// subject key instead of collapsing unrelated deliveries onto "".
func issueEventSubjectAttrs(ev store.JournalEvent) map[string]string {
	attrs := issueSubjectAttrs(ev.After)
	if !isIssueLabelAction(ev.Action) {
		return attrs
	}
	label := strings.TrimSpace(ev.Metadata[issueJournalLabelMetaKey])
	if label == "" {
		return attrs
	}
	if attrs == nil {
		attrs = make(map[string]string, 1)
	}
	attrs[IssueLabelSubjectAttr] = label
	return attrs
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
// of recent consecutive reader failures, decrementing the paused-sweep
// countdown. The window doubles per consecutive failure (recordFailure sets
// 2^min(f,cap)-1 upcoming sweeps to skip), so a flapping reader is retried with
// exponential backoff measured in sweeps — clock-free, like CronScheduler's
// window. A clean poll clears it.
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

// recordSuccess clears the workspace's failure backoff after a clean poll.
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
