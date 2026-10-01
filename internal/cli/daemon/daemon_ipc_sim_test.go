package daemon

// S12 (enforcement map, task 3c748679; model configs d_fail_ipc_only_renewal
// and d_fail_unbound_session_lease) at the FleetDB HTTP seam, through the
// real IPC lease gate: Daemon.handleIPCComplete / handleIPCHeartbeat →
// validateIPCLease → the real fleet-db control-plane client, then the real
// Loom issue adapter, all in front of the fleetsim interposer and fake
// FleetDB, on the fleetsim fake clock (validateLeaseRecord now reads the
// supervisor clock).
//
// The supervisor's ownership acquire/heartbeat, claim, session create and
// worker heartbeat are unexported in package supervisor, so this file issues
// the SAME adapter calls they make (same arguments; ownership and worker
// renewals at a coarser 2m cadence than the supervisor's 30s loops). The
// interposer attributes every request to the attempt whose daemon sent it.
//
// FAKE-ONLY: the control-plane answers are the fake's source-derived model
// (fleetsim/controlplane.go, fleet-db 40e8431d); none was observed on a real
// FleetDB.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor/fleetsim"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	ipcWS          = "LOCALMODE"
	ipcIssue       = "LOCALMODE-3"
	ipcAgent       = "local-coder"
	ipcDaemonActor = "local-mode-harness@fixture.local"
	ipcNodeA       = "loom-supervisor-host-a-101"
	ipcNodeB       = "loom-supervisor-host-b-202"
	ipcTTL         = 30 * time.Minute // supervisor defaultLeaseTTL
)

var ipcEpoch = time.Date(2026, 9, 28, 4, 17, 33, 0, time.UTC)

// ipcAttempt is one attempt of agent local-coder: the daemon serving it (its
// IPC handlers, issue adapter and control-plane client) and its credentials.
type ipcAttempt struct {
	name     string
	node     string
	session  string
	d        *Daemon
	cs       *fleetdb.Client
	ib       *fleet.FleetBackend
	ownToken string
	leaseID  string
	leaseTok string
}

func newIPCAttempt(sim *fleetsim.Sim, name, node string) *ipcAttempt {
	sim.BindAttempt(name, ipcAgent)
	a := &ipcAttempt{name: name, node: node, session: "sess-" + name,
		cs: sim.ControlStore(name, ipcDaemonActor), ib: sim.Backend(name, ipcDaemonActor)}
	a.d = newTestIPCDaemon(&mockIPCBackend{})
	a.d.issueBackend = a.ib
	a.d.store = a.cs
	a.d.sup.WorkspaceID = ipcWS
	a.d.sup.NodeID = node
	a.d.sup.Clock = sim.Clock
	return a
}

func ipcRun(t *testing.T, sim *fleetsim.Sim, name string, fn func()) {
	t.Helper()
	sim.Go(name, func() error { fn(); return nil })
	sim.DrainFIFO()
	if !sim.Finished(name) {
		t.Fatalf("actor %s did not finish", name)
	}
}

// acquire is acquireAgentOwnership's adapter call.
func (a *ipcAttempt) acquire(t *testing.T, sim *fleetsim.Sim) error {
	t.Helper()
	var err error
	ipcRun(t, sim, a.name+"-acquire@"+sim.Clock.Now().Format("150405"), func() {
		var l *domain.AgentOwnershipLease
		l, err = a.cs.AgentOwnershipLeases().Acquire(context.Background(), store.AgentOwnershipLeaseAcquire{
			WorkspaceKey: ipcWS, AgentID: ipcAgent, OwnerID: a.node, RuntimeProvider: domain.RuntimeProviderLocal,
			NodeID: a.node, TTL: ipcTTL})
		if err == nil {
			a.ownToken = l.Token
		}
	})
	return err
}

// start is the rest of a run's preflight: claimIssueForAgent's
// ClaimIssueAsActor and createControlPlaneAgentSession's session + lease.
func (a *ipcAttempt) start(t *testing.T, sim *fleetsim.Sim) {
	t.Helper()
	ctx := context.Background()
	ipcRun(t, sim, a.name+"-start", func() {
		if err := a.ib.ClaimIssueAsActor(ctx, ipcIssue, 0, ipcAgent); err != nil {
			t.Errorf("%s claim: %v", a.name, err)
			return
		}
		if _, err := a.cs.AgentSessions().Create(ctx, store.AgentSessionCreate{WorkspaceKey: ipcWS, SessionID: a.session,
			AgentID: ipcAgent, NodeID: a.node, Kind: domain.AgentSessionKindTask, TaskID: ipcIssue,
			Status: domain.AgentSessionStarting, Phase: "implementation"}); err != nil {
			t.Errorf("%s session: %v", a.name, err)
			return
		}
		l, err := a.cs.AgentLeases().Create(ctx, store.AgentLeaseCreate{WorkspaceKey: ipcWS, SessionID: a.session,
			LeaseID: a.session + "-lease", AgentID: ipcAgent, NodeID: a.node, TTL: ipcTTL})
		if err != nil {
			t.Errorf("%s lease: %v", a.name, err)
			return
		}
		a.leaseID, a.leaseTok = l.LeaseID, l.Token
	})
	if t.Failed() {
		t.FailNow()
	}
}

// keepAlive renews ownership (startOwnershipHeartbeat's call) and the worker
// registration (startWorkerHeartbeatEvery's call) every step in (from, to].
func (a *ipcAttempt) keepAlive(t *testing.T, sim *fleetsim.Sim, from, to time.Duration) {
	t.Helper()
	for at := from + 2*time.Minute; at <= to; at += 2 * time.Minute {
		sim.Clock.Set(ipcEpoch.Add(at))
		ipcRun(t, sim, a.name+"-renew@"+at.String(), func() {
			if _, err := a.cs.AgentOwnershipLeases().Heartbeat(context.Background(), ipcWS, ipcAgent, a.ownToken, ipcTTL); err != nil {
				t.Errorf("%s ownership heartbeat at +%s: %v", a.name, at, err)
			}
			if err := a.cs.Workers().Heartbeat(context.Background(), ipcWS, ipcAgent); err != nil {
				t.Errorf("%s worker heartbeat at +%s: %v", a.name, at, err)
			}
		})
	}
	if t.Failed() {
		t.FailNow()
	}
}

func (a *ipcAttempt) req(op string, args any) AgentIPCRequest {
	r := AgentIPCRequest{Operation: op, AgentName: ipcAgent, IssueID: ipcIssue,
		SessionID: a.session, LeaseID: a.leaseID, LeaseToken: a.leaseTok}
	if args != nil {
		r.Args, _ = json.Marshal(args)
	}
	return r
}

// complete is `loom` completing its task through daemon IPC.
func (a *ipcAttempt) complete(t *testing.T, sim *fleetsim.Sim) AgentIPCResponse {
	t.Helper()
	var resp AgentIPCResponse
	ipcRun(t, sim, a.name+"-ipc-complete@"+sim.Clock.Now().Format("150405"), func() {
		resp = a.d.handleIPCComplete(a.req("complete", map[string]string{"reason": "done"}))
	})
	return resp
}

func ipcViolations(t *testing.T, rep fleetsim.Report, want map[string]int) {
	t.Helper()
	got := map[string]int{}
	for _, v := range rep.Violations {
		got[v.Invariant]++
	}
	if len(got) != len(want) {
		t.Fatalf("violations = %v, want counts %v", rep.Violations, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Fatalf("violations = %v, want counts %v", rep.Violations, want)
		}
	}
}

// S12 (d_fail_ipc_only_renewal), today: the session lease is renewed only
// by IPC mutations. An agent that stays quiet for over 30 minutes while its
// daemon keeps ownership (and the claim lock) alive has its next IPC write
// refused: the lease heartbeat gets 410, the verify-by-get sees expires_at in
// the past, and the gate answers "lease expired". The control writes at 29m.
func TestFleetSim_S12_QuietOwnerLosesSessionLease(t *testing.T) {
	for _, tc := range []struct {
		name  string
		quiet time.Duration
		ok    bool
	}{
		{"quiet 31m (reproduction)", 31 * time.Minute, false},
		{"control: quiet 29m", 29 * time.Minute, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := fleetsim.New(ipcEpoch, ipcWS, fleetsim.Guards{})
			sim.Server.Seed(fleetsim.Issue{ID: ipcIssue, Title: "Local mode coder task", Design: "approved", Priority: 2})
			a1 := newIPCAttempt(sim, "a1", ipcNodeA)
			if err := a1.acquire(t, sim); err != nil {
				t.Fatal(err)
			}
			a1.start(t, sim)
			a1.keepAlive(t, sim, 0, tc.quiet-time.Minute)
			sim.Clock.Set(ipcEpoch.Add(tc.quiet))
			if got := sim.OwnerAuthority(ipcAgent); got != "a1" {
				t.Fatalf("ownership authority = %q, want a1 (owner kept alive)", got)
			}
			resp := a1.complete(t, sim)
			is, _, _ := sim.Server.Snapshot(ipcIssue)
			if tc.ok {
				if !resp.Success || is.Status != "closed" {
					t.Fatalf("control: resp=%+v issue=%s", resp, is.Status)
				}
				ipcViolations(t, sim.Check(), map[string]int{})
				return
			}
			if resp.Success || !strings.Contains(resp.Error, "lease expired") || is.Status != "in_progress" {
				t.Fatalf("quiet owner's IPC write: resp=%+v issue=%s, want refused with lease expired", resp, is.Status)
			}
			ipcViolations(t, sim.Check(), map[string]int{fleetsim.InvLiveOwnerKeepsSessionLease: 1})
		})
	}
}

// S12 (d_fail_unbound_session_lease), today: the session lease is not bound
// to the ownership generation. Daemon A's agent renews its lease at +10m20s;
// daemon A is then paused. Ownership (last renewed +10m) lapses at +40m and
// daemon B acquires it and re-claims the task. When A resumes at +40m10s, an
// IPC complete its agent queued during the pause is served before A's overdue
// ownership heartbeat: the gate checks only the session lease (valid until
// +40m20s), so A's superseded attempt closes the task under B's authority.
// A's heartbeat then gets 403. Control: A is not paused, so B's acquire is
// refused and A's write is the owner's.
func TestFleetSim_S12_SupersededAttemptWritesThroughIPC(t *testing.T) {
	for _, paused := range []bool{true, false} {
		name := "control: daemon A keeps renewing"
		if paused {
			name = "daemon A paused past ownership TTL (reproduction)"
		}
		t.Run(name, func(t *testing.T) {
			sim := fleetsim.New(ipcEpoch, ipcWS, fleetsim.Guards{})
			sim.Server.Seed(fleetsim.Issue{ID: ipcIssue, Title: "Local mode coder task", Design: "approved", Priority: 2})
			a1 := newIPCAttempt(sim, "a1", ipcNodeA)
			if err := a1.acquire(t, sim); err != nil {
				t.Fatal(err)
			}
			a1.start(t, sim)
			a1.keepAlive(t, sim, 0, 10*time.Minute)
			sim.Clock.Set(ipcEpoch.Add(10*time.Minute + 20*time.Second))
			var hb AgentIPCResponse
			ipcRun(t, sim, "a1-ipc-heartbeat", func() { hb = a1.d.handleIPCHeartbeat(a1.req("heartbeat", nil)) })
			if !hb.Success {
				t.Fatalf("a1 IPC heartbeat: %+v", hb)
			}

			if paused {
				// The claim reaper (default on) reverts the task once A's
				// claim lock lapsed (last worker heartbeat +10m, TTL 300s).
				sim.Clock.Set(ipcEpoch.Add(16 * time.Minute))
				if got := sim.Server.ReapStaleClaims(); len(got) != 1 {
					t.Fatalf("claim reaper reverted %v", got)
				}
			} else {
				a1.keepAlive(t, sim, 10*time.Minute, 40*time.Minute)
			}

			b := newIPCAttempt(sim, "b", ipcNodeB)
			sim.Clock.Set(ipcEpoch.Add(40 * time.Minute))
			err := b.acquire(t, sim)
			if !paused {
				if !errors.Is(err, domain.ErrAlreadyClaimed) {
					t.Fatalf("control: b acquire err = %v, want 409 already_claimed", err)
				}
			} else {
				if err != nil {
					t.Fatalf("b acquire at a1's ownership expiry: %v", err)
				}
				b.start(t, sim)
			}

			sim.Clock.Set(ipcEpoch.Add(40*time.Minute + 10*time.Second))
			resp := a1.complete(t, sim)
			is, _, _ := sim.Server.Snapshot(ipcIssue)
			if !resp.Success || is.Status != "closed" {
				t.Fatalf("a1 IPC complete resp=%+v issue=%s, want accepted", resp, is.Status)
			}
			if !paused {
				ipcViolations(t, sim.Check(), map[string]int{})
				return
			}
			var hbErr error
			ipcRun(t, sim, "a1-ownership-heartbeat", func() {
				_, hbErr = a1.cs.AgentOwnershipLeases().Heartbeat(context.Background(), ipcWS, ipcAgent, a1.ownToken, ipcTTL)
			})
			if !errors.Is(hbErr, domain.ErrConflict) || !strings.Contains(hbErr.Error(), "HTTP 403") {
				t.Fatalf("a1 resumed ownership heartbeat err = %v, want 403 (ErrConflict)", hbErr)
			}
			ipcViolations(t, sim.Check(), map[string]int{
				fleetsim.InvNoSupersededSessionWrite: 1,
				fleetsim.InvNoSupersededWrite:        2,
			})
		})
	}
}
