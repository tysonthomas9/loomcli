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
