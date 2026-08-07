package trigger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// taskReadyReconcileEventIDPrefix identifies synthetic current-state events.
// It intentionally differs from fleet-journal-* IDs: reconciliation represents
// the current Ready generation, not a historical journal occurrence.
const taskReadyReconcileEventIDPrefix = "task-ready-reconcile-v1-"

const taskReadyReconcileActor = "system:task-ready-reconcile"

// TaskReadySnapshot is the narrow, consumer-owned projection needed to emit a
// current task.ready state without importing the issue backend into trigger.
// UpdatedAt is the ready-generation anchor. SourceRepo is explicit (including
// empty), while RepositoryRequired separately captures whether that empty value
// is invalid for this workspace; single-repo fallback remains valid.
type TaskReadySnapshot struct {
	TaskID     string
	Status     string
	HasDesign  bool
	Labels     []string
	IssueType  string
	SourceRepo string
	// RepositoryRequired is the pre-read indication that this non-epic task has
	// no explicit source repository and its workspace did not have exactly one
	// repository. It is never dispatch authority: every repo-less task is
	// revalidated by the Work Items owner at commit time when that port exists.
	RepositoryRequired bool
	UpdatedAt          time.Time
}

// TaskReadyIssueLookup resolves the current issue projection used to enrich a
// journal update delta. When configured, an error fails the bridge pass so the
// event is retried without losing repository/phase admission facts.
type TaskReadyIssueLookup func(context.Context, string, string) (TaskReadySnapshot, error)

// TaskReadySnapshotLister returns the canonical current Ready view for one
// workspace. Serve adapts IssueBackend.Ready to this consumer-owned port.
type TaskReadySnapshotLister func(context.Context, string) ([]TaskReadySnapshot, error)

// TaskReadyRepositoryRequiredResult is the consumer-owned outcome of the Work
// Items repository-admission command. Blocked counts only a newly applied
// repository-required transition. DispatchReady carries the canonical
// commit-time task projection when the stale admission request instead found a
// still-ready task whose repository requirement is now satisfied. A zero
// result means the task was already blocked or is no longer ready.
type TaskReadyRepositoryRequiredResult struct {
	Blocked       bool
	DispatchReady *TaskReadySnapshot
}

// TaskReadyRepositoryRequiredBlocker delegates the repository-required policy
// transition to the Work Items owner. The callback must re-read/revalidate the
// current card before mutating it because the supplied task-ready snapshot can
// race a later repository assignment or a workspace repository-count change.
// When DispatchReady is returned, the bridge emits from that canonical
// projection immediately; relying on a later issue event would strand the
// repository-count-change case because that change emits no issue journal row.
type TaskReadyRepositoryRequiredBlocker func(context.Context, string, string) (TaskReadyRepositoryRequiredResult, error)

type taskReadyGeneration struct {
	UpdatedAt time.Time
}

type taskReadyReconcileIdentity struct {
	Workspace    string            `json:"workspace"`
	TaskID       string            `json:"task_id"`
	UpdatedAt    string            `json:"updated_at"`
	ActorRef     string            `json:"actor_ref"`
	SubjectRef   string            `json:"subject_ref"`
	Payload      json.RawMessage   `json:"payload"`
	SubjectAttrs map[string]string `json:"subject_attrs,omitempty"`
}

func (b *IssueJournalBridge) reconcileTaskReadyOnce(ctx context.Context, ws string, out *IssueJournalSweepResult) error {
	if !b.EmitTaskReady || b.ReadySnapshots == nil || b.taskReadyReconciliationDone(ws) {
		return nil
	}

	snapshots, err := b.ReadySnapshots(ctx, ws)
	if err != nil {
		return fmt.Errorf("list current ready tasks in workspace %q: %w", ws, err)
	}
	sort.SliceStable(snapshots, func(i, j int) bool {
		left, right := normalizeTaskReadySnapshot(snapshots[i]), normalizeTaskReadySnapshot(snapshots[j])
		if left.TaskID != right.TaskID {
			return left.TaskID < right.TaskID
		}
		return left.UpdatedAt.Before(right.UpdatedAt)
	})

	seen := make(map[string]struct{}, len(snapshots))
	for _, raw := range snapshots {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		snapshot, blocked, eligible, err := b.admitTaskReadySnapshot(ctx, ws, raw)
		if err != nil {
			return err
		}
		if blocked {
			out.TaskReadyBlocked++
		}
		if !eligible {
			continue
		}
		event, err := taskReadyReconcileEvent(ws, snapshot)
		if err != nil {
			return err
		}
		if _, duplicate := seen[event.EventID]; duplicate {
			continue
		}
		seen[event.EventID] = struct{}{}
		if err := b.emitTaskReadyInternal(ctx, ws, snapshot.TaskID, event); err != nil {
			return err
		}
		b.rememberTaskReadyGeneration(ws, snapshot)
		out.TaskReadyEmitted++
	}
	b.markTaskReadyReconciliationDone(ws)
	return nil
}

func (b *IssueJournalBridge) admitTaskReadySnapshot(
	ctx context.Context,
	ws string,
	raw TaskReadySnapshot,
) (TaskReadySnapshot, bool, bool, error) {
	snapshot := normalizeTaskReadySnapshot(raw)
	if snapshot.TaskID == "" {
		return snapshot, false, false, fmt.Errorf(
			"reconcile current ready tasks in workspace %q: task id is required: %w", ws, domain.ErrInvalid,
		)
	}
	// Ready() is the canonical eligibility gate. Epics are orchestration
	// containers, never prompt-agent claim targets.
	if snapshot.Status != readyIssueStatus || strings.EqualFold(snapshot.IssueType, "epic") {
		return snapshot, false, false, nil
	}
	if !taskReadyNeedsRepositoryAdmission(snapshot) || b.RepositoryRequiredBlocker == nil {
		return snapshot, false, true, nil
	}
	admission, err := b.RepositoryRequiredBlocker(ctx, ws, snapshot.TaskID)
	if err != nil {
		return snapshot, false, false, fmt.Errorf(
			"block repository-required task %q in workspace %q: %w", snapshot.TaskID, ws, err,
		)
	}
	if admission.DispatchReady == nil {
		return snapshot, admission.Blocked, false, nil
	}
	// A repository change may satisfy policy without emitting an issue event, so
	// continue from the command's canonical commit-time projection.
	snapshot = normalizeTaskReadySnapshot(*admission.DispatchReady)
	snapshot.RepositoryRequired = false
	eligible := snapshot.Status == readyIssueStatus && !strings.EqualFold(snapshot.IssueType, "epic")
	return snapshot, admission.Blocked, eligible, nil
}

func taskReadyReconcileEvent(ws string, snapshot TaskReadySnapshot) (InternalEvent, error) {
	snapshot = normalizeTaskReadySnapshot(snapshot)
	payload, err := taskReadySnapshotPayload(snapshot)
	if err != nil {
		return InternalEvent{}, fmt.Errorf("marshal current task.ready payload for %q: %w", snapshot.TaskID, err)
	}
	attrs := map[string]string{"status": readyIssueStatus}
	if snapshot.SourceRepo != "" {
		attrs["repo"] = snapshot.SourceRepo
	}
	subjectRef := IssueJournalSubjectRefPrefix + snapshot.TaskID
	identity, err := json.Marshal(taskReadyReconcileIdentity{
		Workspace: ws, TaskID: snapshot.TaskID,
		UpdatedAt: snapshot.UpdatedAt.Format(time.RFC3339Nano),
		ActorRef:  taskReadyReconcileActor, SubjectRef: subjectRef,
		Payload: payload, SubjectAttrs: attrs,
	})
	if err != nil {
		return InternalEvent{}, fmt.Errorf("marshal current task.ready identity for %q: %w", snapshot.TaskID, err)
	}
	sum := sha256.Sum256(identity)
	return InternalEvent{
		EventID:      taskReadyReconcileEventIDPrefix + hex.EncodeToString(sum[:]),
		EventType:    TaskReadyEventType,
		Origin:       automation.EventOriginSystem,
		ActorRef:     taskReadyReconcileActor,
		SubjectRef:   subjectRef,
		Payload:      payload,
		SubjectAttrs: attrs,
	}, nil
}

// taskReadySnapshotPayload serializes the canonical live projection shared by
// startup reconciliation and the journal lane's commit-time redispatch path.
// Status comes from the projection (rather than an older journal snapshot), so
// callers can never make a non-open task look ready in the emitted payload.
func taskReadySnapshotPayload(snapshot TaskReadySnapshot) (json.RawMessage, error) {
	snapshot = normalizeTaskReadySnapshot(snapshot)
	return json.Marshal(map[string]any{
		"taskId":             snapshot.TaskID,
		"status":             snapshot.Status,
		"hasDesign":          snapshot.HasDesign,
		"labels":             snapshot.Labels,
		"issueType":          snapshot.IssueType,
		"sourceRepo":         snapshot.SourceRepo,
		"repositoryRequired": snapshot.RepositoryRequired,
	})
}

func normalizeTaskReadySnapshot(snapshot TaskReadySnapshot) TaskReadySnapshot {
	snapshot.TaskID = strings.TrimSpace(snapshot.TaskID)
	snapshot.Status = strings.ToLower(strings.TrimSpace(snapshot.Status))
	snapshot.IssueType = strings.TrimSpace(snapshot.IssueType)
	snapshot.SourceRepo = strings.TrimSpace(snapshot.SourceRepo)
	snapshot.Labels = canonicalTaskReadyLabels(snapshot.Labels)
	snapshot.UpdatedAt = snapshot.UpdatedAt.UTC()
	return snapshot
}

func canonicalTaskReadyLabels(labels []string) []string {
	set := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label != "" {
			set[label] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for label := range set {
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}

func (b *IssueJournalBridge) emitTaskReadyInternal(ctx context.Context, ws, taskID string, event InternalEvent) error {
	_, err := b.Source.Emit(ctx, ws, event)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		b.logger().Debug("issue journal bridge: no binding for task.ready event, advancing past it",
			"workspace", ws, "event_id", event.EventID, "task_id", taskID)
		return nil
	default:
		return fmt.Errorf("emit task.ready event %q in workspace %q: %w", event.EventID, ws, err)
	}
}

func (b *IssueJournalBridge) taskReadyReconciliationDone(ws string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.taskReadyReconciled[ws]
}

func (b *IssueJournalBridge) markTaskReadyReconciliationDone(ws string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.taskReadyReconciled == nil {
		b.taskReadyReconciled = make(map[string]bool)
	}
	b.taskReadyReconciled[ws] = true
}

func (b *IssueJournalBridge) rememberTaskReadyGeneration(ws string, snapshot TaskReadySnapshot) {
	if snapshot.UpdatedAt.IsZero() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.taskReadyGenerations == nil {
		b.taskReadyGenerations = make(map[string]map[string]taskReadyGeneration)
	}
	if b.taskReadyGenerations[ws] == nil {
		b.taskReadyGenerations[ws] = make(map[string]taskReadyGeneration)
	}
	current, found := b.taskReadyGenerations[ws][snapshot.TaskID]
	if !found || snapshot.UpdatedAt.After(current.UpdatedAt) {
		b.taskReadyGenerations[ws][snapshot.TaskID] = taskReadyGeneration{UpdatedAt: snapshot.UpdatedAt}
	}
}

func (b *IssueJournalBridge) taskReadyJournalGenerationReconciled(
	ctx context.Context,
	ws string,
	event store.JournalEvent,
) (bool, error) {
	b.mu.Lock()
	workspace := b.taskReadyGenerations[ws]
	generation, found := workspace[strings.TrimSpace(event.EntityID)]
	b.mu.Unlock()
	if !found {
		return false, nil
	}
	if updatedAt, ok := journalSnapshotUpdatedAt(event.After); ok {
		return !updatedAt.After(generation.UpdatedAt), nil
	}

	// Typed release/reopen journal snapshots are intentionally narrow and may
	// omit updated_at. Production has a live issue lookup already used for
	// task.ready enrichment; compare that canonical generation instead of
	// admitting both the startup snapshot and its catch-up journal entry. A
	// newer live generation still emits, and an already-claimed/closed card
	// suppresses the stale ready occurrence.
	if b.IssueLookup == nil {
		return false, nil
	}
	current, err := b.IssueLookup(ctx, ws, event.EntityID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// The reconciled ready generation was deleted before its older
			// journal occurrence reached the cursor. Treat the occurrence as
			// durably stale so it cannot pin every later task in the workspace.
			return true, nil
		}
		return false, fmt.Errorf(
			"look up current task %q for task.ready generation in workspace %q: %w",
			event.EntityID, ws, err,
		)
	}
	current = normalizeTaskReadySnapshot(current)
	if current.Status != readyIssueStatus {
		return true, nil
	}
	if current.UpdatedAt.IsZero() {
		return false, nil
	}
	return !current.UpdatedAt.After(generation.UpdatedAt), nil
}

func journalSnapshotUpdatedAt(after json.RawMessage) (time.Time, bool) {
	fields := snapshotFields(after)
	raw, ok := fields["updated_at"]
	if !ok {
		return time.Time{}, false
	}
	value, ok := scalarString(raw)
	if !ok {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}
