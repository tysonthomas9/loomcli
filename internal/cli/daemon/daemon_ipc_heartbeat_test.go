package daemon

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

func TestIPCDispatch_HeartbeatUpdatesSupervisorActivity(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	d.sup.Agents = []*supervisor.AgentProcess{
		{Entry: config.AgentEntry{Worktree: "worker", Role: "task"}},
	}

	at := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	resp := d.dispatchIPCOperation(AgentIPCRequest{
		Operation:      ipcOpHeartbeat,
		AgentName:      "worker",
		LastActivityAt: at,
	})

	if !resp.Success {
		t.Fatalf("heartbeat dispatch returned error: %s", resp.Error)
	}
	if got := d.sup.Agents[0].LastActivity; !got.Equal(at) {
		t.Errorf("LastActivity = %v, want %v", got, at)
	}
}

func TestIPCDispatch_HeartbeatAllowsEmptyIssueID(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	// validateIPCRequest must NOT reject a heartbeat with no issue_id —
	// the daemon updates per-agent liveness by name, not by issue.
	if _, ok := validateIPCRequest(AgentIPCRequest{
		Operation: ipcOpHeartbeat,
		AgentName: "worker",
	}); !ok {
		t.Fatal("heartbeat without issue_id was rejected; want allowed")
	}
}

func TestIPCDispatch_ClaimPiggybacksActivity(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	d.sup.Agents = []*supervisor.AgentProcess{
		{Entry: config.AgentEntry{Worktree: "worker", Role: "task"}},
	}

	at := time.Date(2026, 5, 21, 12, 5, 0, 0, time.UTC)
	resp := d.dispatchIPCOperation(AgentIPCRequest{
		Operation:      ipcOpClaim,
		AgentName:      "worker",
		IssueID:        "LOOM-99",
		LastActivityAt: at,
	})
	if !resp.Success {
		t.Fatalf("claim returned error: %s", resp.Error)
	}
	if got := d.sup.Agents[0].LastActivity; !got.Equal(at) {
		t.Errorf("LastActivity after claim = %v, want %v (must piggyback)", got, at)
	}
}

func TestIPCDispatch_OutOfOrderHeartbeatDoesNotRegress(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	d.sup.Agents = []*supervisor.AgentProcess{
		{Entry: config.AgentEntry{Worktree: "worker", Role: "task"}},
	}

	t1 := time.Date(2026, 5, 21, 12, 10, 0, 0, time.UTC)
	t0 := time.Date(2026, 5, 21, 12, 9, 0, 0, time.UTC)

	if r := d.dispatchIPCOperation(AgentIPCRequest{
		Operation:      ipcOpHeartbeat,
		AgentName:      "worker",
		LastActivityAt: t1,
	}); !r.Success {
		t.Fatalf("first heartbeat returned error: %s", r.Error)
	}
	if r := d.dispatchIPCOperation(AgentIPCRequest{
		Operation:      ipcOpHeartbeat,
		AgentName:      "worker",
		LastActivityAt: t0,
	}); !r.Success {
		t.Fatalf("second (out-of-order) heartbeat returned error: %s", r.Error)
	}

	if got := d.sup.Agents[0].LastActivity; !got.Equal(t1) {
		t.Errorf("LastActivity = %v, want %v (must NOT regress)", got, t1)
	}
}

func TestIPCDispatch_HeartbeatZeroTimestampIsNoop(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	t0 := time.Date(2026, 5, 21, 12, 15, 0, 0, time.UTC)
	d.sup.Agents = []*supervisor.AgentProcess{
		{Entry: config.AgentEntry{Worktree: "worker", Role: "task"}, LastActivity: t0},
	}

	resp := d.dispatchIPCOperation(AgentIPCRequest{
		Operation: ipcOpHeartbeat,
		AgentName: "worker",
		// LastActivityAt intentionally zero
	})
	if !resp.Success {
		t.Fatalf("heartbeat with zero ts returned error: %s", resp.Error)
	}
	if got := d.sup.Agents[0].LastActivity; !got.Equal(t0) {
		t.Errorf("LastActivity = %v, want %v (zero heartbeat must not regress existing value)", got, t0)
	}
}

// The input-wait signal rides the existing heartbeat op rather than a new
// transport: the phase is read off the request exactly as LastActivityAt is.
func TestIPCDispatch_HeartbeatCarriesInputWaitEdges(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	ap := &supervisor.AgentProcess{Entry: config.AgentEntry{Worktree: "worker", Role: "task"}}
	d.sup.Agents = []*supervisor.AgentProcess{ap}

	beat := func(phase string) {
		t.Helper()
		resp := d.dispatchIPCOperation(AgentIPCRequest{
			Operation: ipcOpHeartbeat,
			AgentName: "worker",
			InputWait: phase,
		})
		if !resp.Success {
			t.Fatalf("heartbeat(input_wait=%q) returned error: %s", phase, resp.Error)
		}
	}

	beat(ipcInputWaitBegin)
	beat(ipcInputWaitBegin)
	if got := pendingInputWaits(ap); got != 2 {
		t.Errorf("InputWaitPending = %d, want 2 after two begins", got)
	}

	beat(ipcInputWaitEnd)
	if got := pendingInputWaits(ap); got != 1 {
		t.Errorf("InputWaitPending = %d, want 1 after one end", got)
	}
}

// An unrecognized phase must not move the counter — a count stuck above zero is
// a suspended watchdog, so version skew has to fail closed.
func TestIPCDispatch_UnknownInputWaitPhaseIsIgnored(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	ap := &supervisor.AgentProcess{Entry: config.AgentEntry{Worktree: "worker", Role: "task"}}
	d.sup.Agents = []*supervisor.AgentProcess{ap}

	resp := d.dispatchIPCOperation(AgentIPCRequest{
		Operation: ipcOpHeartbeat,
		AgentName: "worker",
		InputWait: "paused",
	})
	if !resp.Success {
		t.Fatalf("heartbeat with unknown phase returned error: %s", resp.Error)
	}
	if got := pendingInputWaits(ap); got != 0 {
		t.Errorf("InputWaitPending = %d, want 0 (unknown phase must be ignored)", got)
	}
}

func pendingInputWaits(ap *supervisor.AgentProcess) int {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	return ap.InputWaitPending
}
