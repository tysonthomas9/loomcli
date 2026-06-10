package platformdb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

// The wire shapes below mirror fleet-db's request/response structs
// (internal/api/platform.go on the platform branch) so loom doesn't
// import fleet-db packages directly. platform.* types are already
// JSON-tagged to match, so most calls decode straight into them.

type driverStore struct{ c *Client }

var _ platform.DriverStore = (*driverStore)(nil)

func (s *driverStore) Get(ctx context.Context, ws, driverID string) (*platform.Driver, error) {
	var out platform.Driver
	if err := s.c.do(ctx, "GET", wsPath(ws, "drivers/"+pathEscape(driverID)), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverStore) Create(ctx context.Context, ws string, d platform.Driver) (*platform.Driver, error) {
	var out platform.Driver
	if err := s.c.do(ctx, "POST", wsPath(ws, "drivers"), d, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverStore) CreateVersion(ctx context.Context, ws, driverID string, v platform.DriverVersion) (*platform.DriverVersion, error) {
	var out platform.DriverVersion
	if err := s.c.do(ctx, "POST", wsPath(ws, "drivers/"+pathEscape(driverID)+"/versions"), v, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverStore) Activate(ctx context.Context, ws, driverID, versionID string) (*platform.Driver, error) {
	body := struct {
		ActiveVersionID string `json:"active_version_id"`
	}{ActiveVersionID: versionID}
	var out platform.Driver
	if err := s.c.do(ctx, "PATCH", wsPath(ws, "drivers/"+pathEscape(driverID)), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type driverRunStore struct{ c *Client }

var _ platform.DriverRunStore = (*driverRunStore)(nil)

// ownerBody mirrors fleet-db's driverRunOwnerRequest.
type ownerBody struct {
	NodeID       string `json:"node_id"`
	LeaseID      string `json:"lease_id,omitempty"`
	FencingToken int64  `json:"fencing_token,omitempty"`
}

func (s *driverRunStore) Create(ctx context.Context, ws string, in platform.DriverRunCreate) (*platform.DriverRun, error) {
	var out platform.DriverRun
	if err := s.c.do(ctx, "POST", wsPath(ws, "driver-runs"), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverRunStore) Get(ctx context.Context, ws, runID string) (*platform.DriverRun, error) {
	var out platform.DriverRun
	if err := s.c.do(ctx, "GET", wsPath(ws, "driver-runs/"+pathEscape(runID)), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverRunStore) List(ctx context.Context, ws string, f platform.DriverRunFilter) ([]*platform.DriverRun, error) {
	q := url.Values{}
	if f.DriverID != "" {
		q.Set("driver_id", f.DriverID)
	}
	if f.EpicID != "" {
		q.Set("epic_id", f.EpicID)
	}
	if f.Status != "" {
		q.Set("status", string(f.Status))
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	path := wsPath(ws, "driver-runs")
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		DriverRuns []*platform.DriverRun `json:"driver_runs"`
	}
	if err := s.c.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out.DriverRuns, nil
}

func (s *driverRunStore) Claim(ctx context.Context, ws, runID, nodeID, leaseID string) (*platform.DriverRun, error) {
	var out platform.DriverRun
	body := ownerBody{NodeID: nodeID, LeaseID: leaseID}
	if err := s.c.do(ctx, "POST", wsPath(ws, "driver-runs/"+pathEscape(runID)+"/claim"), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverRunStore) Heartbeat(ctx context.Context, ws, runID, nodeID, leaseID string, fencingToken int64) (*platform.DriverRun, error) {
	var out platform.DriverRun
	body := ownerBody{NodeID: nodeID, LeaseID: leaseID, FencingToken: fencingToken}
	if err := s.c.do(ctx, "POST", wsPath(ws, "driver-runs/"+pathEscape(runID)+"/heartbeat"), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverRunStore) Finish(ctx context.Context, ws, runID, nodeID, leaseID string, fencingToken int64, in platform.DriverRunFinish) (*platform.DriverRun, error) {
	body := struct {
		ownerBody
		platform.DriverRunFinish
	}{
		ownerBody{NodeID: nodeID, LeaseID: leaseID, FencingToken: fencingToken},
		in,
	}
	var out platform.DriverRun
	if err := s.c.do(ctx, "POST", wsPath(ws, "driver-runs/"+pathEscape(runID)+"/finish"), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *driverRunStore) RecoverStale(ctx context.Context, ws string, maxAgeSeconds int64, errorClass, summary string) ([]string, error) {
	body := struct {
		MaxAgeSeconds int64  `json:"max_age_seconds,omitempty"`
		ErrorClass    string `json:"error_class,omitempty"`
		Summary       string `json:"summary,omitempty"`
	}{MaxAgeSeconds: maxAgeSeconds, ErrorClass: errorClass, Summary: summary}
	// Mirrors fleet-db's StaleDriverRunRecoveryResult: `recovered` is a
	// count; the IDs ride in `recovered_run_ids`.
	var out struct {
		RecoveredRunIDs []string `json:"recovered_run_ids"`
	}
	if err := s.c.do(ctx, "POST", wsPath(ws, "driver-runs/recover-stale"), body, &out); err != nil {
		return nil, err
	}
	return out.RecoveredRunIDs, nil
}

func (s *driverRunStore) Events(ctx context.Context, ws, runID, after string, limit int) ([]platform.RunEvent, string, error) {
	q := url.Values{}
	if after != "" {
		q.Set("after", after)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := wsPath(ws, "driver-runs/"+pathEscape(runID)+"/events")
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		Events []platform.RunEvent `json:"events"`
		Cursor string              `json:"cursor"`
	}
	if err := s.c.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, "", err
	}
	return out.Events, out.Cursor, nil
}

type taskRunStore struct{ c *Client }

var _ platform.TaskRunStore = (*taskRunStore)(nil)

func (s *taskRunStore) Create(ctx context.Context, ws string, in platform.TaskRunCreate) (*platform.TaskRun, error) {
	var out platform.TaskRun
	if err := s.c.do(ctx, "POST", wsPath(ws, "task-runs"), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *taskRunStore) List(ctx context.Context, ws string, f platform.TaskRunFilter) ([]*platform.TaskRun, error) {
	q := url.Values{}
	if f.DriverRunID != "" {
		q.Set("driver_run_id", f.DriverRunID)
	}
	if f.TaskID != "" {
		q.Set("task_id", f.TaskID)
	}
	if f.Status != "" {
		q.Set("status", string(f.Status))
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	path := wsPath(ws, "task-runs")
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		TaskRuns []*platform.TaskRun `json:"task_runs"`
	}
	if err := s.c.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out.TaskRuns, nil
}

type ledgerStore struct{ c *Client }

var _ platform.ActionLedgerStore = (*ledgerStore)(nil)

func (s *ledgerStore) Create(ctx context.Context, ws string, in platform.LedgerCreate) (*platform.LedgerEntry, error) {
	var out platform.LedgerEntry
	if err := s.c.do(ctx, "POST", wsPath(ws, "action-ledger"), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *ledgerStore) Complete(ctx context.Context, ws, actionID string, status platform.LedgerStatus) (*platform.LedgerEntry, error) {
	body := struct {
		Status platform.LedgerStatus `json:"status"`
	}{Status: status}
	var out platform.LedgerEntry
	if err := s.c.do(ctx, "POST", wsPath(ws, "action-ledger/"+pathEscape(actionID)+"/complete"), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IssueParent resolves an issue's parent epic ID straight from
// fleet-db ("" when the issue has no parent). The reconciler uses this
// for issue-event wake routing instead of the IssueBackend detail
// path, which currently drops fleet-db's parent_id in conversion.
func (c *Client) IssueParent(ctx context.Context, ws, issueID string) (string, error) {
	var out struct {
		ParentID string `json:"parent_id"`
		Parent   string `json:"parent"`
	}
	if err := c.do(ctx, "GET", wsPath(ws, "issues/"+pathEscape(issueID)), nil, &out); err != nil {
		return "", err
	}
	if out.ParentID != "" {
		return out.ParentID, nil
	}
	return out.Parent, nil
}

type eventStore struct{ c *Client }

var _ platform.EventStore = (*eventStore)(nil)

func (s *eventStore) Poll(ctx context.Context, ws string, req platform.MutationPoll) (*platform.MutationPage, error) {
	q := url.Values{}
	since := req.Since
	if since == "" {
		since = "0"
	}
	q.Set("since", since)
	if req.Timeout > 0 {
		// fleet-db accepts 1000–10000ms.
		ms := req.Timeout.Milliseconds()
		if ms < 1000 {
			ms = 1000
		}
		if ms > 10000 {
			ms = 10000
		}
		q.Set("timeout", strconv.FormatInt(ms, 10))
	}
	if req.Limit > 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	path := wsPath(ws, "events/mutations") + "?" + q.Encode()

	// Give the HTTP round trip headroom beyond the server-side block.
	pollCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		pollCtx, cancel = context.WithTimeout(ctx, req.Timeout+15*time.Second)
		defer cancel()
	}
	var out platform.MutationPage
	if err := s.c.do(pollCtx, "GET", path, nil, &out); err != nil {
		return nil, fmt.Errorf("poll mutations: %w", err)
	}
	return &out, nil
}
