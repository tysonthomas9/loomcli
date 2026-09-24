package stackstore

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

// FleetDBStore implements Store against fleet-db's stack lineage API, the
// canonical stack state in a FleetDB workspace. fleet-db enforces the same
// lineage rules and branch naming as stacklineage, stores each stack as one
// revisioned document, and reports failures that this adapter maps back onto
// the stackstore and stacklineage sentinels so callers need not know which
// store they hold.
type FleetDBStore struct{ api *fleetdb.StackClient }

var (
	_ Store = (*FleetDBStore)(nil)
	_ Mover = (*FleetDBStore)(nil)
)

// NewFleetDB returns a Store backed by fleet-db's stack API.
func NewFleetDB(api *fleetdb.StackClient) *FleetDBStore { return &FleetDBStore{api: api} }

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
	_, err := s.api.Ensure(ctx, in.WorkspaceKey, string(in.ID), fleetdb.StackEnsure{
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

// MoveNode splices taskID to sit immediately after afterTaskID. fleet-db has
// no single move operation, so the splice is a sequence of SetBase writes
// ordered so the lineage is valid (linear, acyclic) after every step:
//
//  1. detach taskID into its own root (its successor rides along),
//  2. reattach that successor to taskID's old base,
//  3. hang afterTaskID's successor onto taskID,
//  4. put taskID after afterTaskID.
//
// The plan is computed and validated against one snapshot before any write.
// A failure part-way leaves a valid lineage with the move incomplete; the
// returned error says so and re-running the move converges.
func (s *FleetDBStore) MoveNode(ctx context.Context, ws string, id sl.StackID, taskID, afterTaskID string) error {
	if taskID == afterTaskID {
		return sl.ErrCycle
	}
	nodes, err := s.ListNodes(ctx, ws, id)
	if err != nil {
		return err
	}
	steps, err := planMove(nodes, taskID, afterTaskID)
	if err != nil {
		return err
	}
	for i, st := range steps {
		if err := s.SetBase(ctx, ws, id, st.taskID, st.base); err != nil {
			if i == 0 {
				return err
			}
			return fmt.Errorf("stackstore: move %s after %s stopped at step %d of %d (lineage is valid; re-run the move): %w",
				taskID, afterTaskID, i+1, len(steps), err)
		}
	}
	return nil
}

type setBaseStep struct{ taskID, base string }

// planMove returns the SetBase steps that splice taskID after afterTaskID, or
// none when it is already there. It rejects the move up front when the result
// would be invalid or would retarget a merged (terminal) node.
func planMove(nodes []sl.Node, taskID, afterTaskID string) ([]setBaseStep, error) {
	byTask := sl.ByTask(nodes)
	node, ok := byTask[taskID]
	if !ok {
		return nil, ErrNodeNotFound
	}
	if _, ok := byTask[afterTaskID]; !ok {
		return nil, ErrNodeNotFound
	}
	if node.BaseTaskID == afterTaskID {
		return nil, nil
	}
	childOf := func(base, except string) string {
		for _, n := range nodes {
			if n.BaseTaskID == base && n.TaskID != except {
				return n.TaskID
			}
		}
		return ""
	}
	oldBase := node.BaseTaskID
	succ := childOf(taskID, "") // rides with taskID in step 1
	// Step 2 never changes afterTaskID's successor: it only repoints succ,
	// whose base becomes oldBase, and afterTaskID == oldBase returned above.
	afterSucc := childOf(afterTaskID, taskID)

	var steps []setBaseStep
	if oldBase != "" {
		steps = append(steps, setBaseStep{taskID, ""})
	}
	if succ != "" {
		steps = append(steps, setBaseStep{succ, oldBase})
	}
	if afterSucc != "" {
		steps = append(steps, setBaseStep{afterSucc, taskID})
	}
	steps = append(steps, setBaseStep{taskID, afterTaskID})

	if err := validateMovePlan(nodes, steps); err != nil {
		return nil, err
	}
	return steps, nil
}

// validateMovePlan checks the lineage after steps and refuses to retarget
// merged nodes, before any write, so a doomed move changes nothing.
func validateMovePlan(nodes []sl.Node, steps []setBaseStep) error {
	byTask := sl.ByTask(nodes)
	final := make(map[string]string, len(nodes))
	for _, n := range nodes {
		final[n.TaskID] = n.BaseTaskID
	}
	for _, st := range steps {
		if byTask[st.taskID].State.Terminal() && final[st.taskID] != st.base {
			return ErrNodeTerminal
		}
		final[st.taskID] = st.base
	}
	result := make([]sl.Node, 0, len(nodes))
	for _, n := range nodes {
		n.BaseTaskID = final[n.TaskID]
		result = append(result, n)
	}
	_, err := sl.Ordered(result)
	return err
}

// patch construction -----------------------------------------------------------

// nodePatch diffs fn's result against the node it was given.
func nodePatch(cur, next sl.Node) (fleetdb.StackNodePatch, bool, error) {
	if next.TaskID != cur.TaskID || next.StackID != cur.StackID ||
		next.BaseTaskID != cur.BaseTaskID || next.OutputBranch != cur.OutputBranch {
		return fleetdb.StackNodePatch{}, false, fmt.Errorf("%w: lineage and identity fields (taskId, stackId, baseTaskId, outputBranch) are not writable through UpdateNode", ErrUnsupportedUpdate)
	}
	if cur.LastPublishedAt != nil && next.LastPublishedAt == nil {
		return fleetdb.StackNodePatch{}, false, fmt.Errorf("%w: lastPublishedAt cannot be cleared", ErrUnsupportedUpdate)
	}
	var p fleetdb.StackNodePatch
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
	var apiErr *fleetdb.StackAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Status == http.StatusPreconditionFailed ||
		(apiErr.Status == http.StatusConflict && apiErr.Code == "conflict")
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
	var apiErr *fleetdb.StackAPIError
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

func stackFromWire(w fleetdb.StackWire) sl.Stack {
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

func nodesFromWire(id sl.StackID, in []fleetdb.StackNodeWire) []sl.Node {
	if len(in) == 0 {
		return nil
	}
	out := make([]sl.Node, 0, len(in))
	for _, n := range in {
		out = append(out, nodeFromWire(id, n))
	}
	return out
}

func nodeFromWire(id sl.StackID, w fleetdb.StackNodeWire) sl.Node {
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
