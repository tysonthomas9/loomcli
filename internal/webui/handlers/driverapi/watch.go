package driverapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// Watch stream cadence. The poll interval drives journal reads, the
// heartbeat keeps intermediaries from idling the connection out, and the
// reconciliation interval re-sends a full snapshot AND re-verifies parent
// run liveness so a long stream never outlives its driver run.
const (
	defaultWatchPollInterval      = time.Second
	defaultWatchHeartbeatInterval = 15 * time.Second
	defaultWatchReconcileInterval = 60 * time.Second

	// watchEventBatchLimit caps how many journal events one poll drains.
	watchEventBatchLimit = 100
	// watchActiveTaskRunsLimit caps the active-run list inside snapshots.
	watchActiveTaskRunsLimit = 100
)

// watchSnapshotData is the data payload of a "snapshot" SSE frame: the epic's
// scheduling state plus the parent run's active task runs. Both component
// types are already camelCase json-tagged.
type watchSnapshotData struct {
	Epic   *driverpkg.EpicSnapshot   `json:"epic"`
	Active *driverpkg.ActiveTaskRuns `json:"active"`
}

// handleWatchEpic serves GET /api/workspaces/{ws}/driver/watch/epic: an SSE
// stream of TaskRun journal events scoped to an epic. Contract:
//
//   - handshake: "event: snapshot" carrying watchSnapshotData, id = cursor
//   - journal:   "event: taskRun" per event, id = the event's Seq
//   - resume:    Last-Event-ID header (or afterSeq query) as an exclusive
//     int64 Seq cursor — already-seen events are skipped
//   - liveness:  every reconciliation tick re-verifies the parent run and
//     re-sends a snapshot; when the parent is no longer running the stream
//     ends with "event: closed" {code: "parent_not_running"}
//
// Errors before streaming starts use the structured {code,message,retryable}
// envelope.
func (m *Module) handleWatchEpic(w http.ResponseWriter, r *http.Request) {
	session, snapshot, ok := m.prepareWatch(w, r)
	if !ok {
		return
	}
	sw, err := realtime.NewWriter(w)
	if err != nil {
		writeOpError(w, http.StatusInternalServerError, "internal", "streaming unsupported", false)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	// Long-lived stream: the server-wide write deadline must not apply.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	if sw.WriteRetry(realtime.RetryMs) != nil {
		return
	}
	if writeWatchSnapshot(sw, session.cursor, snapshot) != nil {
		return
	}
	m.runWatchLoop(r.Context(), sw, session)
}

// prepareWatch authenticates the watch request and resolves the per-stream
// state plus the handshake snapshot. Everything that can fail runs here,
// before the response commits to the SSE content type, so failures still
// produce a structured JSON error instead of a dead stream. On failure the
// error is already written and ok is false.
func (m *Module) prepareWatch(w http.ResponseWriter, r *http.Request) (watchSession, *watchSnapshotData, bool) {
	tokenID, ok := m.authenticate(w, r)
	if !ok {
		return watchSession{}, nil, false
	}
	ws := r.PathValue("ws")
	id, ok := requestIdentity(w, r, tokenID)
	if !ok {
		return watchSession{}, nil, false
	}
	ctx := r.Context()
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		writeDomainOpError(w, err)
		return watchSession{}, nil, false
	}
	epicID := firstNonEmpty(r.URL.Query().Get("epicId"), parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload))
	if epicID == "" {
		writeOpError(w, http.StatusBadRequest, "invalid", "epicId required (query or driver run payload)", false)
		return watchSession{}, nil, false
	}
	cursor, err := watchCursor(r)
	if err != nil {
		writeDomainOpError(w, err)
		return watchSession{}, nil, false
	}
	issueBackend, err := m.issueBackends(ws, driverpkg.DriverRunActor(parent.RunID))
	if err != nil {
		writeDomainOpError(w, err)
		return watchSession{}, nil, false
	}
	snapshot, err := m.loadWatchSnapshot(ctx, issueBackend, ws, parent.RunID, epicID)
	if err != nil {
		writeDomainOpError(w, err)
		return watchSession{}, nil, false
	}
	return watchSession{
		ws:           ws,
		epicID:       epicID,
		id:           id,
		driverRunID:  parent.RunID,
		issueBackend: issueBackend,
		cursor:       cursor,
	}, snapshot, true
}

// watchSession carries the resolved per-stream state into the watch loop.
type watchSession struct {
	ws           string
	epicID       string
	id           driverIdentity
	driverRunID  string
	issueBackend backend.IssueBackend
	cursor       int64
}

// runWatchLoop drives an established watch stream until the client
// disconnects, a write fails, or the parent run stops running.
func (m *Module) runWatchLoop(ctx context.Context, sw *realtime.Writer, session watchSession) {
	poll := time.NewTicker(m.watchPollInterval)
	defer poll.Stop()
	heartbeat := time.NewTicker(m.watchHeartbeatInterval)
	defer heartbeat.Stop()
	reconcile := time.NewTicker(m.watchReconcileInterval)
	defer reconcile.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			next, err := m.streamWatchEvents(ctx, sw, session.ws, session.epicID, session.cursor)
			if err != nil {
				return
			}
			session.cursor = next
		case <-heartbeat.C:
			if sw.WriteComment("heartbeat") != nil {
				return
			}
		case <-reconcile.C:
			// Re-verify parent liveness so the stream cannot outlive its run.
			if _, err := m.verifyParent(ctx, session.ws, session.id); err != nil {
				if watchParentNotRunning(err) {
					writeWatchClosed(sw, session.cursor, "parent_not_running")
				}
				return
			}
			// Reconciliation snapshot guards against any dropped append.
			snapshot, err := m.loadWatchSnapshot(ctx, session.issueBackend, session.ws, session.driverRunID, session.epicID)
			if err != nil {
				return
			}
			if writeWatchSnapshot(sw, session.cursor, snapshot) != nil {
				return
			}
		}
	}
}

// watchCursor parses the resume cursor: Last-Event-ID header first (SSE
// reconnect), afterSeq query as the explicit alternative. Zero means "from
// the beginning of the journal".
func watchCursor(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("afterSeq"))
	}
	if raw == "" {
		return 0, nil
	}
	seq, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seq < 0 {
		return 0, fmt.Errorf("parse watch cursor %q as non-negative int64 seq: %w", raw, domain.ErrInvalid)
	}
	return seq, nil
}

// loadWatchSnapshot assembles the snapshot frame payload.
func (m *Module) loadWatchSnapshot(ctx context.Context, issueBackend backend.IssueBackend, ws, driverRunID, epicID string) (*watchSnapshotData, error) {
	epic, err := driverpkg.LoadEpicSnapshot(ctx, issueBackend, driverpkg.EpicSnapshotOptions{EpicID: epicID})
	if err != nil {
		return nil, fmt.Errorf("snapshot epic: %w", err)
	}
	active, err := driverpkg.ListActiveTaskRuns(ctx, m.store, driverpkg.ActiveTaskRunsOptions{
		WorkspaceKey: ws,
		DriverRunID:  driverRunID,
		EpicID:       epicID,
		Limit:        watchActiveTaskRunsLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("list active task runs: %w", err)
	}
	return &watchSnapshotData{Epic: epic, Active: active}, nil
}

// streamWatchEvents drains journal events after the cursor and writes each as
// an "event: taskRun" frame whose id is the event Seq. Returns the advanced
// cursor; a non-nil error means the stream should end.
func (m *Module) streamWatchEvents(ctx context.Context, sw *realtime.Writer, ws, epicID string, afterSeq int64) (int64, error) {
	events, err := m.store.TaskRunEvents().ListSince(ctx, ws, store.TaskRunEventFilter{
		EpicID:   epicID,
		AfterSeq: afterSeq,
		Limit:    watchEventBatchLimit,
	})
	if err != nil {
		return afterSeq, err
	}
	cursor := afterSeq
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			return cursor, err
		}
		if err := sw.WriteEvent(event.Seq, "taskRun", string(data)); err != nil {
			return cursor, err
		}
		cursor = event.Seq
	}
	return cursor, nil
}

func writeWatchSnapshot(sw *realtime.Writer, cursor int64, snapshot *watchSnapshotData) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return sw.WriteEvent(cursor, "snapshot", string(data))
}

func writeWatchClosed(sw *realtime.Writer, cursor int64, code string) {
	data, _ := json.Marshal(map[string]string{"code": code})
	_ = sw.WriteEvent(cursor, "closed", string(data))
}

// watchParentNotRunning reports whether a verifyParent failure means the
// parent run is definitively gone (finished, deleted, or lease lost) rather
// than a transient store error. Only definitive failures get the explicit
// "closed" frame; transient errors just drop the connection so the client
// reconnects with its cursor.
func watchParentNotRunning(err error) bool {
	return errors.Is(err, domain.ErrNotFound) ||
		errors.Is(err, domain.ErrNotOwner) ||
		errors.Is(err, domain.ErrInvalidTransition)
}
