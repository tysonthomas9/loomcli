package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

const (
	// claimHoldFileName is the on-disk record of the workspace claim hold. It
	// lives beside daemon.pid and, unlike the PID and state files, is NOT
	// removed on shutdown: surviving a daemon restart is the entire point.
	claimHoldFileName = "claim-hold.json"

	// defaultClaimHoldTTL is the TTL applied when --ttl is not given. A
	// forgotten hold self-releases rather than parking the fleet for hours.
	defaultClaimHoldTTL = 60 * time.Minute
	minClaimHoldTTL     = time.Minute
	maxClaimHoldTTL     = 24 * time.Hour

	// corruptClaimHoldTTL bounds the fail-safe hold synthesized when
	// claim-hold.json cannot be parsed: hold (so a quiesce is never silently
	// dropped), but never forever.
	corruptClaimHoldTTL = 15 * time.Minute

	// claimHoldStaleAfter is the age at which a still-active hold renders with
	// an escalated marker in `loom daemon status`.
	claimHoldStaleAfter = 2 * time.Hour

	claimHoldWaitPollInterval = 5 * time.Second
	defaultClaimHoldWaitLimit = 30 * time.Minute
)

// ClaimHoldRunningAgent describes one agent whose process is still alive while
// a hold is active. A hold never touches a running agent; these are reported so
// an operator (or `--wait-idle`) can see what a quiesce is still waiting on.
type ClaimHoldRunningAgent struct {
	Agent     string    `json:"agent"`
	TaskID    string    `json:"task_id,omitempty"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

// ClaimHoldStatus is the payload of both claims_hold_set and claims_hold_get.
type ClaimHoldStatus struct {
	Hold    *supervisor.ClaimHold   `json:"hold"`
	Running []ClaimHoldRunningAgent `json:"running"`
	Gated   int                     `json:"gated"`
}

// claimHoldSetArgs is the args object of the claims_hold_set operation.
type claimHoldSetArgs struct {
	Held       bool   `json:"held"`
	Actor      string `json:"actor"`
	Reason     string `json:"reason"`
	TTLSeconds int64  `json:"ttl_seconds"`
	Force      bool   `json:"force"`
}

// resolveClaimHoldPath returns the claim-hold file path for a daemon whose PID
// file is at pidFile — the same directory the daemon lock lives in.
func resolveClaimHoldPath(pidFile string) string {
	return filepath.Join(filepath.Dir(pidFile), claimHoldFileName)
}

// readClaimHoldFile reads and parses the claim-hold record. A missing file is
// reported as (nil, nil): no hold.
func readClaimHoldFile(path string) (*supervisor.ClaimHold, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path constructed from the known .loom directory
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var h supervisor.ClaimHold
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if !h.Held {
		return nil, nil
	}
	return &h, nil
}

// writeClaimHoldFile atomically writes the claim-hold record, or removes it
// when h is nil (or not held). The temp name carries the PID so two writers
// cannot clobber each other's partial file.
func writeClaimHoldFile(path string, h *supervisor.ClaimHold) error {
	if h == nil || !h.Held {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	tempFile := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tempFile, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tempFile, path); err != nil {
		os.Remove(tempFile)
		return err
	}
	return nil
}

// loadClaimHoldAtStartup reads the persisted hold, failing SAFE but BOUNDED: a
// corrupt or unreadable record becomes a hold owned by "unknown" with a short
// synthetic expiry, so a quiesce is never silently dropped and never becomes
// permanent.
func loadClaimHoldAtStartup(path string) *supervisor.ClaimHold {
	h, err := readClaimHoldFile(path)
	if err == nil {
		return h
	}
	slog.Error("claim-hold file unreadable; holding claims on a short fail-safe expiry",
		"path", path, "ttl", corruptClaimHoldTTL, "err", err)
	now := time.Now()
	return &supervisor.ClaimHold{
		Held:      true,
		Actor:     "unknown",
		Reason:    fmt.Sprintf("unreadable %s (fail-safe hold)", claimHoldFileName),
		Since:     now,
		ExpiresAt: now.Add(corruptClaimHoldTTL),
	}
}

// hydrateClaimHold loads the persisted hold into the supervisor and wires the
// persistence hook. Called before the supervisor starts, so no agent can cycle
// past the gate before the hold is in place.
func hydrateClaimHold(d *Daemon, path string) {
	d.sup.PersistClaimHold = func(h *supervisor.ClaimHold) error {
		return writeClaimHoldFile(path, h)
	}
	if h := loadClaimHoldAtStartup(path); h != nil {
		d.sup.LoadClaimHold(h)
		slog.Warn("claim hold restored from disk; the daemon will not start new work",
			"actor", h.Actor, "reason", h.Reason, "expires_at", h.ExpiresAt)
	}
}

// claimHoldStatus builds the payload both socket operations return.
func (d *Daemon) claimHoldStatus() ClaimHoldStatus {
	status := ClaimHoldStatus{Hold: d.sup.ClaimHoldSnapshot(), Running: []ClaimHoldRunningAgent{}}
	for _, a := range d.sup.GetAgents() {
		if a.ClaimsGated {
			status.Gated++
		}
		if a.PID > 0 && lockfile.IsProcessRunning(a.PID) {
			status.Running = append(status.Running, ClaimHoldRunningAgent{
				Agent:     a.Worktree,
				TaskID:    a.AssignedTaskID,
				PID:       a.PID,
				StartedAt: a.LastStart,
			})
		}
	}
	return status
}

// claimHoldResponse marshals a ClaimHoldStatus into a control response.
func claimHoldResponse(status ClaimHoldStatus) DaemonControlResponse {
	data, err := json.Marshal(status)
	if err != nil {
		return DaemonControlResponse{Error: fmt.Sprintf("marshal claim hold status: %v", err)}
	}
	return DaemonControlResponse{Success: true, Data: data}
}

// handleClaimHoldGet answers the claims_hold_get operation.
func (d *Daemon) handleClaimHoldGet() DaemonControlResponse {
	return claimHoldResponse(d.claimHoldStatus())
}

// handleClaimHoldSet applies or releases the workspace claim hold.
//
// Held=false releases, refusing a foreign holder without force. Held=true by
// the SAME actor is an idempotent refresh: Since is preserved while Reason and
// ExpiresAt are updated.
func (d *Daemon) handleClaimHoldSet(raw json.RawMessage) DaemonControlResponse {
	var args claimHoldSetArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return DaemonControlResponse{Error: "invalid claim hold args: " + err.Error()}
		}
	}
	if args.Actor == "" {
		return DaemonControlResponse{Error: "actor is required"}
	}

	if !args.Held {
		if err := d.sup.ReleaseClaimHold(args.Actor, args.Force); err != nil {
			return DaemonControlResponse{Error: err.Error()}
		}
		slog.Info("claim hold released", "actor", args.Actor, "force", args.Force)
		return claimHoldResponse(d.claimHoldStatus())
	}

	if args.Reason == "" {
		return DaemonControlResponse{Error: "reason is required to hold claims"}
	}
	now := time.Now()
	hold := &supervisor.ClaimHold{Held: true, Actor: args.Actor, Reason: args.Reason, Since: now}
	if args.TTLSeconds > 0 {
		hold.ExpiresAt = now.Add(time.Duration(args.TTLSeconds) * time.Second)
	}

	if current := d.sup.ClaimHoldSnapshot(); current.Active(now) {
		if current.Actor != args.Actor && !args.Force {
			return DaemonControlResponse{Error: fmt.Sprintf(
				"claims held by %s since %s; use --force to replace", current.Actor, current.Since.Format(time.RFC3339))}
		}
		if current.Actor == args.Actor {
			hold.Since = current.Since // idempotent refresh
		}
	}

	persistErr := d.sup.SetClaimHold(hold)
	status := d.claimHoldStatus()
	if persistErr != nil {
		// The hold IS applied in memory; the operator must learn it will not
		// survive a daemon restart rather than believe it is durable.
		resp := claimHoldResponse(status)
		resp.Success = false
		resp.Error = fmt.Sprintf("claim hold applied in memory but NOT persisted: %v", persistErr)
		return resp
	}
	slog.Info("claim hold set", "actor", hold.Actor, "reason", hold.Reason,
		"expires", hold.ExpiresAt, "running", len(status.Running))
	return claimHoldResponse(status)
}

// ── CLI ─────────────────────────────────────────────────────────────────────

var (
	daemonHoldReason   string
	daemonHoldActor    string
	daemonHoldTTL      time.Duration
	daemonHoldWaitIdle bool
	daemonHoldTimeout  time.Duration
	daemonHoldForce    bool

	daemonReleaseActor string
	daemonReleaseForce bool
)

// daemonHoldCmd stops the daemon from STARTING new work without touching any
// run already in flight.
var daemonHoldCmd = &cobra.Command{
	Use:   "hold",
	Short: "Refuse to start new work without touching running agents",
	Long: `Set a workspace claim hold: the daemon stops claiming tasks and starting
agents, while every run already in flight continues untouched.

No yield file is written, no signal is sent, and no deadline is imposed on a
running agent — the hold gates the claim path only, and performs no fleet-db
calls, so it works while fleet-db itself is being redeployed.

The hold survives a daemon restart and applies to THIS daemon (this workspace),
not to the fleet.

Examples:
  loom daemon hold --reason "deploy union tips"
  loom daemon hold --actor union-autodeploy --reason "deploy $SHA" --ttl 45m --wait-idle`,
	RunE: runDaemonHold,
}

// daemonReleaseCmd clears the workspace claim hold.
var daemonReleaseCmd = &cobra.Command{
	Use:   "release",
	Short: "Release the workspace claim hold",
	Long: `Clear the workspace claim hold so agents resume claiming work.

Releasing a hold owned by a different actor requires --force.`,
	RunE: runDaemonRelease,
}

func init() {
	daemonHoldCmd.Flags().StringVar(&daemonHoldReason, "reason", "", "why claims are held (required)")
	daemonHoldCmd.Flags().StringVar(&daemonHoldActor, "actor", "", "who owns the hold (default: $LOOM_ACTOR, else the OS user)")
	daemonHoldCmd.Flags().DurationVar(&daemonHoldTTL, "ttl", defaultClaimHoldTTL, "auto-release after this long (0 = indefinite)")
	daemonHoldCmd.Flags().BoolVar(&daemonHoldWaitIdle, "wait-idle", false, "after setting the hold, wait until no agent is running")
	daemonHoldCmd.Flags().DurationVar(&daemonHoldTimeout, "timeout", defaultClaimHoldWaitLimit, "how long --wait-idle waits before giving up")
	daemonHoldCmd.Flags().BoolVar(&daemonHoldForce, "force", false, "replace a hold owned by a different actor")

	daemonReleaseCmd.Flags().StringVar(&daemonReleaseActor, "actor", "", "who is releasing (default: $LOOM_ACTOR, else the OS user)")
	daemonReleaseCmd.Flags().BoolVar(&daemonReleaseForce, "force", false, "release a hold owned by a different actor")
}

// resolveHoldActor resolves the hold's owner: the flag, else $LOOM_ACTOR, else
// the OS user, else "unknown".
func resolveHoldActor(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("LOOM_ACTOR"); env != "" {
		return env
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if env := os.Getenv("USER"); env != "" {
		return env
	}
	return "unknown"
}

// validateHoldTTL clamps the requested TTL to [1m, 24h]. Zero means indefinite
// and is returned as-is with a warning: the operator asked for the one shape
// that cannot self-heal.
func validateHoldTTL(ttl time.Duration) (time.Duration, error) {
	if ttl < 0 {
		return 0, fmt.Errorf("--ttl must not be negative")
	}
	if ttl == 0 {
		fmt.Fprintln(os.Stderr, "Warning: --ttl 0 holds claims indefinitely; nothing will release it but you.")
		return 0, nil
	}
	if ttl < minClaimHoldTTL {
		return minClaimHoldTTL, nil
	}
	if ttl > maxClaimHoldTTL {
		return maxClaimHoldTTL, nil
	}
	return ttl, nil
}

// requestClaimHold sends one claims_hold_set / claims_hold_get round trip.
func requestClaimHold(op string, args any) (*ClaimHoldStatus, error) {
	socketPath, err := resolveControlSocketFromCwd()
	if err != nil {
		return nil, err
	}
	req := DaemonControlRequest{Operation: op}
	if args != nil {
		raw, mErr := json.Marshal(args)
		if mErr != nil {
			return nil, mErr
		}
		req.Args = raw
	}
	resp, err := sendDaemonControlRequestFull(socketPath, req)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var status ClaimHoldStatus
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &status); err != nil {
			return nil, fmt.Errorf("unmarshal claim hold status: %w", err)
		}
	}
	return &status, nil
}

func runDaemonHold(cmd *cobra.Command, args []string) error {
	_, _ = cmd, args
	if daemonHoldReason == "" {
		return fmt.Errorf("--reason is required: an unexplained hold is the failure mode this command exists to prevent")
	}
	ttl, err := validateHoldTTL(daemonHoldTTL)
	if err != nil {
		return err
	}
	actor := resolveHoldActor(daemonHoldActor)

	status, err := requestClaimHold(ctrlOpClaimHoldSet, claimHoldSetArgs{
		Held:       true,
		Actor:      actor,
		Reason:     daemonHoldReason,
		TTLSeconds: int64(ttl / time.Second),
		Force:      daemonHoldForce,
	})
	if err != nil {
		return err
	}

	printClaimHoldStatus(*status)
	if !daemonHoldWaitIdle {
		return nil
	}
	return waitForClaimHoldIdle(daemonHoldTimeout)
}

// waitForClaimHoldIdle polls claims_hold_get until no agent process is left
// running. On timeout it returns an error WITHOUT releasing the hold — whether
// to give up on a quiesce is the operator's decision, not this command's.
func waitForClaimHoldIdle(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	fmt.Printf("Waiting up to %s for running agents to finish...\n", timeout)
	for {
		status, err := requestClaimHold(ctrlOpClaimHoldGet, nil)
		if err != nil {
			return err
		}
		if len(status.Running) == 0 {
			fmt.Println("All agents idle; workspace is quiesced.")
			return nil
		}
		if !time.Now().Before(deadline) {
			fmt.Fprintf(os.Stderr, "Timed out after %s with %d agent(s) still running (hold left in place):\n", timeout, len(status.Running))
			for _, r := range status.Running {
				fmt.Fprintf(os.Stderr, "  %s (PID %d) task %s\n", r.Agent, r.PID, orDash(r.TaskID))
			}
			return fmt.Errorf("timed out waiting for %d running agent(s)", len(status.Running))
		}
		time.Sleep(claimHoldWaitPollInterval)
	}
}

func runDaemonRelease(cmd *cobra.Command, args []string) error {
	_, _ = cmd, args
	actor := resolveHoldActor(daemonReleaseActor)
	status, err := requestClaimHold(ctrlOpClaimHoldSet, claimHoldSetArgs{
		Held:  false,
		Actor: actor,
		Force: daemonReleaseForce,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Claim hold released by %s. Agents resume claiming within ~%s.\n", actor, defaultClaimHoldRecheckHint)
	printClaimHoldStatus(*status)
	return nil
}

// defaultClaimHoldRecheckHint mirrors the supervisor's fixed re-check interval
// for user-facing copy.
const defaultClaimHoldRecheckHint = 15 * time.Second

// printClaimHoldStatus renders a hold and what it is still waiting on.
func printClaimHoldStatus(status ClaimHoldStatus) {
	if !status.Hold.Active(time.Now()) {
		fmt.Println("Claims: not held")
		return
	}
	fmt.Println(claimHoldBanner(status.Hold))
	fmt.Printf("  Gated agents: %d\n", status.Gated)
	if len(status.Running) == 0 {
		fmt.Println("  Running agents: none (workspace is quiesced)")
		return
	}
	fmt.Printf("  Running agents: %d (untouched by the hold)\n", len(status.Running))
	for _, r := range status.Running {
		fmt.Printf("    %s (PID %d) task %s\n", r.Agent, r.PID, orDash(r.TaskID))
	}
}

// claimHoldBanner renders the one-line hold summary used by both `hold` and
// `daemon status`. A hold older than claimHoldStaleAfter is marked as possibly
// forgotten; an indefinite hold is marked as having no backstop.
func claimHoldBanner(h *supervisor.ClaimHold) string {
	age := time.Since(h.Since)
	marker := "HELD"
	if age > claimHoldStaleAfter {
		marker = fmt.Sprintf("WARN HELD %s — forgotten?", formatDaemonDuration(age))
	}
	expires := "never (no backstop)"
	if !h.ExpiresAt.IsZero() {
		expires = fmt.Sprintf("%s (in %s)", h.ExpiresAt.Format(time.RFC3339), formatDaemonDuration(time.Until(h.ExpiresAt)))
	}
	return fmt.Sprintf("Claims: %s by %s since %s — %s; expires %s",
		marker, h.Actor, h.Since.Format(time.RFC3339), h.Reason, expires)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// printClaimHoldBanner renders the hold above the agent table in
// `loom daemon status`. The node scope is stated explicitly: a hold is
// per-daemon/per-workspace and must not be read as fleet-wide.
func printClaimHoldBanner(h *supervisor.ClaimHold) {
	if !h.Active(time.Now()) {
		return
	}
	fmt.Println(claimHoldBanner(h))
	fmt.Println("  Hold applies to THIS daemon (this workspace), not the fleet.")
	fmt.Println("  Running agents are untouched; no new work will be claimed until release.")
}
