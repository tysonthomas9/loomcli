package stackstore

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore/stackwire"
)

// FleetDBStore implements Store against fleet-db's stack lineage API, the
// canonical stack state in a FleetDB workspace. fleet-db enforces the same
// lineage rules and branch naming as stacklineage, stores each stack as one
// revisioned document, and reports failures that this adapter maps back onto
// the stackstore and stacklineage sentinels so callers need not know which
// store they hold.
type FleetDBStore struct{ api stackwire.API }

var (
	_ Store = (*FleetDBStore)(nil)
	_ Mover = (*FleetDBStore)(nil)
)

// NewFleetDB returns a Store backed by fleet-db's stack API.
func NewFleetDB(api stackwire.API) *FleetDBStore { return &FleetDBStore{api: api} }

// updateNodeAttempts bounds UpdateNode's read-modify-CAS loop. A stack is one
// revisioned document, so every write to any node of the stack can cost a
// concurrent UpdateNode a retry; with jittered backoff this comfortably covers
// a parallel epic's finalize fan-in.
const updateNodeAttempts = 16

// updateNodeBackoff is the pause before retry attempt n (n >= 1): linear with
// full jitter, capped at 200ms. A variable so tests can make it zero.
var updateNodeBackoff = func(n int) time.Duration {
	ceiling := min(time.Duration(n)*20*time.Millisecond, 200*time.Millisecond)
	return time.Duration(rand.Int64N(int64(ceiling) + 1)) //nolint:gosec // jitter, not security
}

// reads ----------------------------------------------------------------------

func (s *FleetDBStore) GetStack(ctx context.Context, ws string, id sl.StackID) (*sl.Stack, error) {
	st, err := s.api.Get(ctx, ws, string(id))
	if err != nil {
		return nil, mapFleetErr(err)
	}
	out := stackFromWire(*st)
	return &out, nil
}

func (s *FleetDBStore) ListStacks(ctx context.Context, ws string) ([]sl.Stack, error) {
	stacks, err := s.api.List(ctx, ws)
	if err != nil {
		return nil, mapFleetErr(err)
	}
	if len(stacks) == 0 {
		return nil, nil
	}
	out := make([]sl.Stack, 0, len(stacks))
	for _, st := range stacks {
		out = append(out, stackFromWire(st))
	}
	return out, nil
}

func (s *FleetDBStore) ListNodes(ctx context.Context, ws string, id sl.StackID) ([]sl.Node, error) {
	nodes, err := s.api.ListNodes(ctx, ws, string(id))
	if err != nil {
		return nil, mapFleetErr(err)
	}
	return nodesFromWire(id, nodes), nil
}

// writes ---------------------------------------------------------------------

// EnsureStack creates the stack header if absent, or updates RepoName,
// RootBase and (when non-empty) DefaultCommitMode if present.
func (s *FleetDBStore) EnsureStack(ctx context.Context, in sl.Stack) error {
	if in.WorkspaceKey == "" || in.ID == "" {
		return errors.New("stackstore: stack workspaceKey and id are required")
	}
	_, err := s.api.Ensure(ctx, in.WorkspaceKey, string(in.ID), stackwire.Ensure{
		RepoName:          in.RepoName,
		RootBase:          in.RootBase,
		DefaultCommitMode: string(in.DefaultCommitMode),
	})
	return mapFleetErr(err)
}

func (s *FleetDBStore) DeleteStack(ctx context.Context, ws string, id sl.StackID) error {
	return mapFleetErr(s.api.Delete(ctx, ws, string(id)))
}

// AddNode registers taskID; fleet-db assigns the collision-free output branch
// and validates the lineage atomically with the write.
func (s *FleetDBStore) AddNode(ctx context.Context, ws string, id sl.StackID, taskID, baseTaskID string, mode sl.CommitMode) (sl.Node, error) {
	n, err := s.api.AddNode(ctx, ws, string(id), taskID, baseTaskID, string(mode))
	if err != nil {
		return sl.Node{}, mapFleetErr(err)
	}
	return nodeFromWire(id, *n), nil
}

func (s *FleetDBStore) SetBase(ctx context.Context, ws string, id sl.StackID, taskID, baseTaskID string) error {
	_, err := s.api.SetBase(ctx, ws, string(id), taskID, baseTaskID)
	return mapFleetErr(err)
}

// RemoveNode drops taskID; fleet-db reparents its successor onto its
// predecessor in the same write.
func (s *FleetDBStore) RemoveNode(ctx context.Context, ws string, id sl.StackID, taskID string) error {
	return mapFleetErr(s.api.RemoveNode(ctx, ws, string(id), taskID))
}

// UpdateNode applies fn to the current node and writes the result as a
// compare-and-set on the node's revision, re-reading and re-applying fn when
// another writer got there first. The write is therefore atomic with respect
// to the state fn observed, like LocalStore's locked read-modify-write, but fn
// may run more than once and must depend only on the node it is given.
//
// Only publish state is writable this way (State, CommitMode, PRNumber, PRURL,
// OutputSHA, LastPublishedAt). Lineage and identity changes go through
// SetBase/AddNode/RemoveNode; fn changing them, or clearing LastPublishedAt,
// is rejected with ErrUnsupportedUpdate and nothing is written.
func (s *FleetDBStore) UpdateNode(ctx context.Context, ws string, id sl.StackID, taskID string, fn func(*sl.Node) error) error {
	var lastErr error
	for attempt := range updateNodeAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("stackstore: update node %s: %w", taskID, ctx.Err())
			case <-time.After(updateNodeBackoff(attempt)):
			}
		}
		cur, rev, err := s.readNode(ctx, ws, id, taskID)
		if err != nil {
			return err
		}
		next := cur
		if cur.LastPublishedAt != nil {
			t := *cur.LastPublishedAt
			next.LastPublishedAt = &t
		}
		if err := fn(&next); err != nil {
			return err
		}
		patch, changed, err := nodePatch(cur, next)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		patch.ExpectedRevision = &rev
		_, err = s.api.UpdateNode(ctx, ws, string(id), taskID, patch)
		if err == nil {
			return nil
		}
		if !isRetryableWrite(err) {
			return mapFleetErr(err)
		}
		lastErr = err
	}
	return fmt.Errorf("stackstore: update node %s: %w: %w", taskID, ErrConcurrentUpdate, mapFleetErr(lastErr))
}

func (s *FleetDBStore) readNode(ctx context.Context, ws string, id sl.StackID, taskID string) (sl.Node, int64, error) {
	nodes, err := s.api.ListNodes(ctx, ws, string(id))
	if err != nil {
		return sl.Node{}, 0, mapFleetErr(err)
	}
	for _, n := range nodes {
		if n.TaskID == taskID {
			return nodeFromWire(id, n), n.Revision, nil
		}
	}
	return sl.Node{}, 0, ErrNodeNotFound
}

// moveOutcomeReconcileAttempts bounds re-reads after a 503 stack_inconsistent
// MoveNode response. That status can mean the journal accepted the write before
// projection caught up; we only confirm intent via Get, never by re-issuing Move.
const moveOutcomeReconcileAttempts = 8

// MoveNode splices taskID to sit immediately after afterTaskID via fleet-db's
// atomic, revision-fenced MoveNode endpoint (one stack-document compare-and-set).
// A mid-splice failure cannot leave a half-applied lineage: either the whole
// move commits or the stack revision is unchanged. When another writer advances
// the stack between the read and the fenced write, the call retries with the
// fresh revision (same bound as UpdateNode).
//
// When fleet-db returns 503 stack_inconsistent after the fenced Move (journal
// accepted, projection not yet visible), MoveNode boundedly re-reads and
// succeeds only if the requested node is already immediately after afterTaskID
// at an observed revision strictly newer than the fenced one. It never
// re-issues Move in that path (would overwrite a concurrent writer) and never
// treats a merely "similar" later topology as success. Unconfirmed outcomes
// surface as ErrUnknownWriteOutcome with safe retry guidance.
func (s *FleetDBStore) MoveNode(ctx context.Context, ws string, id sl.StackID, taskID, afterTaskID string) error {
	if taskID == afterTaskID {
		return sl.ErrCycle
	}
	var lastErr error
	for attempt := range updateNodeAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("stackstore: move %s after %s: %w", taskID, afterTaskID, ctx.Err())
			case <-time.After(updateNodeBackoff(attempt)):
			}
		}
		st, err := s.api.Get(ctx, ws, string(id))
		if err != nil {
			return mapFleetErr(err)
		}
		rev := st.Revision
		_, err = s.api.MoveNode(ctx, ws, string(id), taskID, stackwire.MoveRequest{
			AfterTaskID:      afterTaskID,
			ExpectedRevision: &rev,
		})
		if err == nil {
			return nil
		}
		if isStackInconsistent(err) {
			return s.reconcileMoveOutcome(ctx, ws, id, taskID, afterTaskID, rev, err)
		}
		if !isRetryableWrite(err) {
			return mapFleetErr(err)
		}
		lastErr = err
	}
	return fmt.Errorf("stackstore: move %s after %s: %w: %w", taskID, afterTaskID, ErrConcurrentUpdate, mapFleetErr(lastErr))
}

// reconcileMoveOutcome confirms an accepted-but-unprojected Move without
// writing again. fencedRev is the expected_revision sent with the Move that
// returned stack_inconsistent.
func (s *FleetDBStore) reconcileMoveOutcome(ctx context.Context, ws string, id sl.StackID, taskID, afterTaskID string, fencedRev int64, cause error) error {
	var lastErr = cause
	for attempt := range moveOutcomeReconcileAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("stackstore: move %s after %s: %w: %w", taskID, afterTaskID, ErrUnknownWriteOutcome, ctx.Err())
			case <-time.After(updateNodeBackoff(attempt)):
			}
		}
		st, err := s.api.Get(ctx, ws, string(id))
		if err != nil {
			if isStackInconsistent(err) {
				lastErr = err
				continue
			}
			return mapFleetErr(err)
		}
		baseOK := false
		for _, n := range st.Nodes {
			if n.TaskID == taskID {
				baseOK = n.BaseTaskID == afterTaskID
				break
			}
		}
		if baseOK && st.Revision > fencedRev {
			return nil
		}
		if st.Revision > fencedRev {
			// Concurrent writer advanced the document; intent is not met.
			// Do not re-issue Move — that would fence against a foreign revision.
			return fmt.Errorf("stackstore: move %s after %s: %w: observed revision %d after fenced %d without confirming position; re-read and retry only if still unmet: %w",
				taskID, afterTaskID, ErrUnknownWriteOutcome, st.Revision, fencedRev, cause)
		}
		lastErr = cause
	}
	return fmt.Errorf("stackstore: move %s after %s: %w: could not confirm after accepted write; re-read before retrying: %w",
		taskID, afterTaskID, ErrUnknownWriteOutcome, lastErr)
}

// patch construction -----------------------------------------------------------

// nodePatch diffs fn's result against the node it was given.
func nodePatch(cur, next sl.Node) (stackwire.NodePatch, bool, error) {
	if next.TaskID != cur.TaskID || next.StackID != cur.StackID ||
		next.BaseTaskID != cur.BaseTaskID || next.OutputBranch != cur.OutputBranch {
		return stackwire.NodePatch{}, false, fmt.Errorf("%w: lineage and identity fields (taskId, stackId, baseTaskId, outputBranch) are not writable through UpdateNode", ErrUnsupportedUpdate)
	}
	if cur.LastPublishedAt != nil && next.LastPublishedAt == nil {
		return stackwire.NodePatch{}, false, fmt.Errorf("%w: lastPublishedAt cannot be cleared", ErrUnsupportedUpdate)
	}
	var p stackwire.NodePatch
	changed := false
	if next.State != cur.State {
		v := string(next.State)
		p.State, changed = &v, true
	}
	if next.CommitMode != cur.CommitMode {
		v := string(next.CommitMode)
		p.CommitMode, changed = &v, true
	}
	if next.PRNumber != cur.PRNumber {
		v := next.PRNumber
		p.PRNumber, changed = &v, true
	}
	if next.PRURL != cur.PRURL {
		v := next.PRURL
		p.PRURL, changed = &v, true
	}
	if next.OutputSHA != cur.OutputSHA {
		v := next.OutputSHA
		p.OutputSHA, changed = &v, true
	}
	if next.LastPublishedAt != nil && (cur.LastPublishedAt == nil || !next.LastPublishedAt.Equal(*cur.LastPublishedAt)) {
		v := next.LastPublishedAt.UTC()
		p.LastPublishedAt, changed = &v, true
	}
	return p, changed, nil
}

// error mapping ----------------------------------------------------------------

// isRetryableWrite reports a lost revision race: a failed expected_revision
// precondition (412) or fleet-db's own CAS contention (409 conflict).
func isRetryableWrite(err error) bool {
	var apiErr *stackwire.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Status == http.StatusPreconditionFailed ||
		(apiErr.Status == http.StatusConflict && apiErr.Code == "conflict")
}

// isStackInconsistent reports fleet-db's 503 stack_inconsistent: the journal
// may have accepted a stack write that projection has not yet made readable.
func isStackInconsistent(err error) bool {
	var apiErr *stackwire.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Status == http.StatusServiceUnavailable && apiErr.Code == "stack_inconsistent"
}

// lineageRules maps fleet-db's lineage rule messages onto the stacklineage
// sentinels LocalStore returns for the same violation.
var lineageRules = []struct {
	text string
	err  error
}{
	{"cycle", sl.ErrCycle},
	{"base task not found", sl.ErrMissingPredecessor},
	{"multiple successors", sl.ErrBranching},
	{"no root", sl.ErrNoRoot},
}

// mapFleetErr translates a stack API failure into the stackstore/stacklineage
// sentinel for the same condition, keeping the fleet-db error in the chain
// (errors.Is matches both). Errors with no stackstore equivalent pass through.
func mapFleetErr(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *stackwire.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	msg := strings.ToLower(apiErr.Message)
	var sentinel error
	switch apiErr.Status {
	case http.StatusNotFound:
		switch {
		case strings.Contains(msg, "stack node not found"):
			sentinel = ErrNodeNotFound
		case strings.Contains(msg, "stack not found"):
			sentinel = ErrStackNotFound
		}
	case http.StatusConflict:
		switch apiErr.Code {
		case "already_exists":
			sentinel = ErrNodeExists
		case "invalid_transition":
			sentinel = ErrNodeTerminal
		case "conflict":
			sentinel = ErrConcurrentUpdate
		}
	case http.StatusPreconditionFailed:
		sentinel = ErrConcurrentUpdate
	case http.StatusUnprocessableEntity:
		for _, r := range lineageRules {
			if strings.Contains(msg, "invalid stack lineage") && strings.Contains(msg, r.text) {
				sentinel = r.err
				break
			}
		}
	}
	if sentinel == nil {
		return err
	}
	return fmt.Errorf("%w: %w", sentinel, err)
}

// wire conversion ----------------------------------------------------------------

func stackFromWire(w stackwire.Stack) sl.Stack {
	return sl.Stack{
		ID:                sl.StackID(w.ID),
		WorkspaceKey:      w.WorkspaceKey,
		RepoName:          w.RepoName,
		RootBase:          w.RootBase,
		DefaultCommitMode: sl.CommitMode(w.DefaultCommitMode),
		CreatedAt:         w.CreatedAt,
		UpdatedAt:         w.UpdatedAt,
	}
}

func nodesFromWire(id sl.StackID, in []stackwire.Node) []sl.Node {
	if len(in) == 0 {
		return nil
	}
	out := make([]sl.Node, 0, len(in))
	for _, n := range in {
		out = append(out, nodeFromWire(id, n))
	}
	return out
}

func nodeFromWire(id sl.StackID, w stackwire.Node) sl.Node {
	return sl.Node{
		StackID:         id,
		TaskID:          w.TaskID,
		BaseTaskID:      w.BaseTaskID,
		OutputBranch:    w.OutputBranch,
		CommitMode:      sl.CommitMode(w.CommitMode),
		State:           sl.NodeState(w.State),
		PRNumber:        w.PRNumber,
		PRURL:           w.PRURL,
		OutputSHA:       w.OutputSHA,
		LastPublishedAt: w.LastPublishedAt,
		CreatedAt:       w.CreatedAt,
		UpdatedAt:       w.UpdatedAt,
	}
}
