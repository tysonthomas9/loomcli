package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

func setArgs(t *testing.T, a claimHoldSetArgs) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}

func decodeHoldStatus(t *testing.T, resp DaemonControlResponse) ClaimHoldStatus {
	t.Helper()
	var status ClaimHoldStatus
	if err := json.Unmarshal(resp.Data, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	return status
}

// ── on-disk format ──────────────────────────────────────────────────────────

func TestClaimHoldFile_RoundTripAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, claimHoldFileName)

	if h, err := readClaimHoldFile(path); err != nil || h != nil {
		t.Fatalf("missing file = (%v, %v), want (nil, nil)", h, err)
	}

	want := &supervisor.ClaimHold{
		Held: true, Actor: "union-autodeploy", Reason: "deploy union tips",
		Since:     time.Now().Truncate(time.Second),
		ExpiresAt: time.Now().Add(time.Hour).Truncate(time.Second),
	}
	if err := writeClaimHoldFile(path, want); err != nil {
		t.Fatalf("writeClaimHoldFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("mode = %v, want 0600", perm)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("atomic write left a temp file behind: %s", e.Name())
		}
	}

	got, err := readClaimHoldFile(path)
	if err != nil {
		t.Fatalf("readClaimHoldFile: %v", err)
	}
	if got == nil || got.Actor != want.Actor || got.Reason != want.Reason ||
		!got.Since.Equal(want.Since) || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}

	// nil clears the file — persistence must not outlive the hold.
	if err := writeClaimHoldFile(path, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still present after clear: %v", err)
	}
	if err := writeClaimHoldFile(path, nil); err != nil {
		t.Fatalf("clearing an absent file must be a no-op, got %v", err)
	}
}

func TestClaimHoldFile_CorruptIsBoundedFailSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), claimHoldFileName)
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}

	if _, err := readClaimHoldFile(path); err == nil {
		t.Fatal("corrupt file parsed without error")
	}

	h := loadClaimHoldAtStartup(path)
	if h == nil || !h.Held {
		t.Fatal("corrupt file was silently ignored; it must fail safe by holding")
	}
	if h.Actor != "unknown" {
		t.Fatalf("Actor = %q, want unknown", h.Actor)
	}
	if h.ExpiresAt.IsZero() {
		t.Fatal("fail-safe hold is indefinite; it must be bounded")
	}
	if d := time.Until(h.ExpiresAt); d > corruptClaimHoldTTL+time.Minute {
		t.Fatalf("fail-safe expiry in %s, want ≤ %s", d, corruptClaimHoldTTL)
	}
}

func TestLoadClaimHoldAtStartup_ReleasedRecordIsNoHold(t *testing.T) {
	path := filepath.Join(t.TempDir(), claimHoldFileName)
	if err := os.WriteFile(path, []byte(`{"held":false,"actor":"oleh"}`), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if h := loadClaimHoldAtStartup(path); h != nil {
		t.Fatalf("released record loaded as a hold: %#v", h)
	}
}

func TestResolveClaimHoldPath_SitsBesideThePIDFile(t *testing.T) {
	got := resolveClaimHoldPath("/tmp/proj/.loom/daemon.pid")
	if want := "/tmp/proj/.loom/claim-hold.json"; got != want {
		t.Fatalf("resolveClaimHoldPath = %q, want %q", got, want)
	}
}

// ── socket operations ───────────────────────────────────────────────────────

func newHoldTestDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
		{Worktree: "beta", Role: "task"},
	})
	t.Cleanup(func() { d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) }) })
	path := filepath.Join(t.TempDir(), claimHoldFileName)
	hydrateClaimHold(d, path)
	return d, path
}

func TestHandleClaimHoldSet_HoldsPersistsAndReports(t *testing.T) {
	d, path := newHoldTestDaemon(t)

	resp := d.handleClaimHoldSet(setArgs(t, claimHoldSetArgs{
		Held: true, Actor: "union-autodeploy", Reason: "deploy", TTLSeconds: 3600,
	}))
	if !resp.Success {
		t.Fatalf("set failed: %s", resp.Error)
	}
	status := decodeHoldStatus(t, resp)
	if status.Hold == nil || !status.Hold.Active(time.Now()) {
		t.Fatalf("hold not active: %#v", status.Hold)
	}
	if status.Running == nil {
		t.Fatal("running must be an empty list, not null")
	}

	onDisk, err := readClaimHoldFile(path)
	if err != nil || onDisk == nil {
		t.Fatalf("hold not persisted: (%v, %v)", onDisk, err)
	}
	if onDisk.Actor != "union-autodeploy" {
		t.Fatalf("persisted actor = %q", onDisk.Actor)
	}

	// get mirrors set
	got := decodeHoldStatus(t, d.handleClaimHoldGet())
	if got.Hold == nil || got.Hold.Actor != "union-autodeploy" {
		t.Fatalf("get = %#v", got.Hold)
	}
}

func TestHandleClaimHoldSet_RequiresActorAndReason(t *testing.T) {
	d, _ := newHoldTestDaemon(t)

	if resp := d.handleClaimHoldSet(setArgs(t, claimHoldSetArgs{Held: true, Reason: "x"})); resp.Success {
		t.Fatal("set succeeded without an actor")
	}
	if resp := d.handleClaimHoldSet(setArgs(t, claimHoldSetArgs{Held: true, Actor: "oleh"})); resp.Success {
		t.Fatal("set succeeded without a reason; an unexplained hold is the failure mode")
	}
	if resp := d.handleClaimHoldSet(json.RawMessage("{not json")); resp.Success {
		t.Fatal("malformed args accepted")
	}
}

func TestHandleClaimHoldSet_IdempotentReHoldPreservesSince(t *testing.T) {
	d, _ := newHoldTestDaemon(t)

	first := decodeHoldStatus(t, d.handleClaimHoldSet(setArgs(t, claimHoldSetArgs{
		Held: true, Actor: "union-autodeploy", Reason: "deploy a", TTLSeconds: 600,
	})))
	time.Sleep(10 * time.Millisecond)
	second := decodeHoldStatus(t, d.handleClaimHoldSet(setArgs(t, claimHoldSetArgs{
		Held: true, Actor: "union-autodeploy", Reason: "deploy b", TTLSeconds: 1200,
	})))

	if !second.Hold.Since.Equal(first.Hold.Since) {
		t.Fatalf("Since moved on re-hold: %s → %s", first.Hold.Since, second.Hold.Since)
	}
	if second.Hold.Reason != "deploy b" {
		t.Fatalf("Reason = %q, want the refreshed value", second.Hold.Reason)
	}
	if !second.Hold.ExpiresAt.After(first.Hold.ExpiresAt) {
		t.Fatalf("ExpiresAt not refreshed: %s → %s", first.Hold.ExpiresAt, second.Hold.ExpiresAt)
	}
}

func TestHandleClaimHoldSet_ForeignActorNeedsForce(t *testing.T) {
	d, _ := newHoldTestDaemon(t)
	d.handleClaimHoldSet(setArgs(t, claimHoldSetArgs{Held: true, Actor: "union-autodeploy", Reason: "deploy", TTLSeconds: 600}))

	resp := d.handleClaimHoldSet(setArgs(t, claimHoldSetArgs{Held: true, Actor: "oleh", Reason: "mine", TTLSeconds: 600}))
	if resp.Success {
		t.Fatal("foreign re-hold succeeded without force")
	}
	if !strings.Contains(resp.Error, "union-autodeploy") {
		t.Fatalf("error must name the holder: %q", resp.Error)
	}

	resp = d.handleClaimHoldSet(setArgs(t, claimHoldSetArgs{Held: false, Actor: "oleh"}))
	if resp.Success {
		t.Fatal("foreign release succeeded without force")
	}
	if !strings.Contains(resp.Error, "--force") {
		t.Fatalf("error must point at --force: %q", resp.Error)
	}

	resp = d.handleClaimHoldSet(setArgs(t, claimHoldSetArgs{Held: false, Actor: "oleh", Force: true}))
	if !resp.Success {
		t.Fatalf("forced release failed: %s", resp.Error)
	}
	if h := decodeHoldStatus(t, resp).Hold; h.Active(time.Now()) {
		t.Fatalf("hold still active after forced release: %#v", h)
	}
}

func TestHandleClaimHoldSet_ReleaseClearsTheFile(t *testing.T) {
	d, path := newHoldTestDaemon(t)
	d.handleClaimHoldSet(setArgs(t, claimHoldSetArgs{Held: true, Actor: "oleh", Reason: "deploy", TTLSeconds: 600}))

	if resp := d.handleClaimHoldSet(setArgs(t, claimHoldSetArgs{Held: false, Actor: "oleh"})); !resp.Success {
		t.Fatalf("release failed: %s", resp.Error)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("claim-hold file survived release: %v", err)
	}
}

func TestClaimHold_ExpiryClearsTheFile(t *testing.T) {
	d, path := newHoldTestDaemon(t)
	d.sup.LoadClaimHold(&supervisor.ClaimHold{
		Held: true, Actor: "oleh", Reason: "stale",
		Since:     time.Now().Add(-time.Hour),
		ExpiresAt: time.Now().Add(-time.Second),
	})
	if err := writeClaimHoldFile(path, &supervisor.ClaimHold{Held: true, Actor: "oleh", Reason: "stale", Since: time.Now()}); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	status := decodeHoldStatus(t, d.handleClaimHoldGet())
	if status.Hold != nil {
		t.Fatalf("expired hold reported as active: %#v", status.Hold)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired hold did not clear its file: %v", err)
	}
}

func TestHandleAgentControlStart_RefusedWhileHeld(t *testing.T) {
	d, _ := newHoldTestDaemon(t)
	d.handleClaimHoldSet(setArgs(t, claimHoldSetArgs{Held: true, Actor: "union-autodeploy", Reason: "deploy", TTLSeconds: 600}))

	resp := d.handleAgentControlStart("alpha")
	if resp.Success {
		t.Fatal("agent_start succeeded while claims are held")
	}
	if !strings.Contains(resp.Error, "claims held by union-autodeploy") {
		t.Fatalf("error must name the holder: %q", resp.Error)
	}
}

func TestClaimHoldSocketOps_EndToEnd(t *testing.T) {
	socketPath := filepath.Join(shortSocketDir(t), "daemon.sock")
	d, _ := newHoldTestDaemon(t)
	if err := d.startControlServer(socketPath); err != nil {
		t.Fatalf("startControlServer: %v", err)
	}
	t.Cleanup(func() {
		if d.controlListener != nil {
			_ = d.controlListener.Close()
		}
	})

	resp, err := sendDaemonControlRequestFull(socketPath, DaemonControlRequest{
		Operation: ctrlOpClaimHoldSet,
		Args:      setArgs(t, claimHoldSetArgs{Held: true, Actor: "oleh", Reason: "deploy", TTLSeconds: 600}),
	})
	if err != nil {
		t.Fatalf("set over socket: %v", err)
	}
	if !resp.Success {
		t.Fatalf("set over socket failed: %s", resp.Error)
	}

	resp, err = sendDaemonControlRequestFull(socketPath, DaemonControlRequest{Operation: ctrlOpClaimHoldGet})
	if err != nil {
		t.Fatalf("get over socket: %v", err)
	}
	status := decodeHoldStatus(t, *resp)
	if status.Hold == nil || status.Hold.Actor != "oleh" {
		t.Fatalf("get over socket = %#v", status.Hold)
	}

	resp, err = sendDaemonControlRequestFull(socketPath, DaemonControlRequest{
		Operation: ctrlOpAgentStart, AgentName: "alpha",
	})
	if err != nil {
		t.Fatalf("start over socket: %v", err)
	}
	if resp.Success {
		t.Fatal("agent_start over socket succeeded while held")
	}
}

// ── CLI helpers ─────────────────────────────────────────────────────────────

func TestResolveHoldActor_Precedence(t *testing.T) {
	t.Setenv("LOOM_ACTOR", "env-actor")
	if got := resolveHoldActor("flag-actor"); got != "flag-actor" {
		t.Fatalf("flag must win: %q", got)
	}
	if got := resolveHoldActor(""); got != "env-actor" {
		t.Fatalf("LOOM_ACTOR must win over the OS user: %q", got)
	}
	t.Setenv("LOOM_ACTOR", "")
	if got := resolveHoldActor(""); got == "" {
		t.Fatal("actor resolution returned empty; it must always name someone")
	}
}

func TestValidateHoldTTL_Clamps(t *testing.T) {
	cases := []struct {
		in, want time.Duration
	}{
		{defaultClaimHoldTTL, defaultClaimHoldTTL},
		{time.Second, minClaimHoldTTL},
		{48 * time.Hour, maxClaimHoldTTL},
		{0, 0}, // indefinite, deliberately allowed
	}
	for _, c := range cases {
		got, err := validateHoldTTL(c.in)
		if err != nil {
			t.Fatalf("validateHoldTTL(%s): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("validateHoldTTL(%s) = %s, want %s", c.in, got, c.want)
		}
	}
	if _, err := validateHoldTTL(-time.Minute); err == nil {
		t.Fatal("negative TTL accepted")
	}
}

func TestClaimHoldBanner_MarksStaleAndIndefiniteHolds(t *testing.T) {
	fresh := &supervisor.ClaimHold{
		Held: true, Actor: "oleh", Reason: "deploy",
		Since: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
	if b := claimHoldBanner(fresh); strings.Contains(b, "forgotten") {
		t.Fatalf("fresh hold marked as forgotten: %q", b)
	}

	stale := &supervisor.ClaimHold{
		Held: true, Actor: "oleh", Reason: "deploy",
		Since: time.Now().Add(-claimHoldStaleAfter - time.Minute),
	}
	b := claimHoldBanner(stale)
	if !strings.Contains(b, "forgotten") {
		t.Fatalf("stale hold not escalated: %q", b)
	}
	if !strings.Contains(b, "never (no backstop)") {
		t.Fatalf("indefinite hold not flagged: %q", b)
	}
}

// ── state file ──────────────────────────────────────────────────────────────

func TestWriteStateFile_CarriesTheHoldAndGatedFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon-agents.json")
	hold := &supervisor.ClaimHold{Held: true, Actor: "oleh", Reason: "deploy", Since: time.Now()}
	agents := []supervisor.SupervisedAgentStatus{
		{Worktree: "alpha", Role: "plan", ClaimsGated: true},
		{Worktree: "beta", Role: "task"},
	}

	if err := writeStateFile(path, time.Now(), agents, nil, 3, hold); err != nil {
		t.Fatalf("writeStateFile: %v", err)
	}
	state, err := ReadStateFile(path)
	if err != nil {
		t.Fatalf("ReadStateFile: %v", err)
	}
	if state.ClaimHold == nil || state.ClaimHold.Actor != "oleh" {
		t.Fatalf("ClaimHold = %#v", state.ClaimHold)
	}
	if !state.Agents[0].ClaimsGated || state.Agents[1].ClaimsGated {
		t.Fatalf("ClaimsGated projection wrong: %#v", state.Agents)
	}

	// The variadic is optional: omitting it must leave the hold absent.
	if err := writeStateFile(path, time.Now(), agents, nil, 3); err != nil {
		t.Fatalf("writeStateFile without hold: %v", err)
	}
	state, err = ReadStateFile(path)
	if err != nil {
		t.Fatalf("ReadStateFile: %v", err)
	}
	if state.ClaimHold != nil {
		t.Fatalf("ClaimHold = %#v, want nil", state.ClaimHold)
	}
}

// waitIdleDaemon starts a daemon whose control socket is discoverable from the
// process working directory, which is what --wait-idle polls.
func waitIdleDaemon(t *testing.T, runningPID int) *Daemon {
	t.Helper()
	projectDir := shortSocketDir(t)
	if err := os.MkdirAll(filepath.Join(projectDir, ".loom"), 0755); err != nil {
		t.Fatalf("mkdir .loom: %v", err)
	}
	t.Chdir(projectDir)

	d, _ := newHoldTestDaemon(t)
	if runningPID > 0 {
		d.sup.Agents[0].Pid = runningPID
	}
	socketPath, err := resolveControlSocketFromCwd()
	if err != nil {
		t.Fatalf("resolveControlSocketFromCwd: %v", err)
	}
	if err := d.startControlServer(socketPath); err != nil {
		t.Fatalf("startControlServer: %v", err)
	}
	t.Cleanup(func() {
		if d.controlListener != nil {
			_ = d.controlListener.Close()
		}
	})
	return d
}

func TestWaitForClaimHoldIdle_ReturnsOnceNoAgentIsRunning(t *testing.T) {
	waitIdleDaemon(t, 0)
	if err := waitForClaimHoldIdle(10 * time.Second); err != nil {
		t.Fatalf("waitForClaimHoldIdle with no running agents: %v", err)
	}
}

func TestWaitForClaimHoldIdle_TimesOutWithoutReleasingTheHold(t *testing.T) {
	d := waitIdleDaemon(t, os.Getpid())
	if resp := d.handleClaimHoldSet(setArgs(t, claimHoldSetArgs{
		Held: true, Actor: "union-autodeploy", Reason: "deploy", TTLSeconds: 600,
	})); !resp.Success {
		t.Fatalf("set: %s", resp.Error)
	}

	err := waitForClaimHoldIdle(time.Millisecond)
	if err == nil {
		t.Fatal("wait-idle succeeded with an agent still running")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	// The hold must survive: whether to give up on a quiesce is the
	// operator's decision, not this command's.
	if h := d.sup.ClaimHoldSnapshot(); !h.Active(time.Now()) {
		t.Fatal("wait-idle timeout released the hold")
	}
}
