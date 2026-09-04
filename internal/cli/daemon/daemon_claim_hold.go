package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
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

	claimHoldSocketPoll = 500 * time.Millisecond
)

// claimHoldSocketWait is how long a control request waits for a daemon that is
// starting up to bind its control socket. The daemon writes daemon.pid before
// cmdstore.OpenStore and the supervisor start, so the socket can lag the PID
// file by several seconds (measured 9s, PUPPET-200); 30s is headroom over that.
//
// A var, not a const, only so a test can shorten it; nothing at runtime writes
// to it.
var claimHoldSocketWait = 30 * time.Second

// errNoDaemonRunning reports that no daemon process is believed alive for this
// workspace, so waiting for a control socket is pointless and the caller may
// fall back to the file. Callers MUST match it with errors.Is, never on the
// message: the offline path clears an operator's hold, and a string compare
// against an unrelated error is how that happens by accident.
var errNoDaemonRunning = errors.New("no daemon is running for this workspace")

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
	Held       bool     `json:"held"`
	Actor      string   `json:"actor"`
	Reason     string   `json:"reason"`
	TTLSeconds int64    `json:"ttl_seconds"`
	Force      bool     `json:"force"`
	Repos      []string `json:"repos,omitempty"` // empty = workspace-wide
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

// claimHoldStat identifies the CONTENT of claim-hold.json cheaply: (mtime,
// size). The zero value means "no file", which is itself a state worth
// distinguishing — deleting the file is exactly how a release without a control
// socket lifts a hold.
type claimHoldStat struct {
	exists  bool
	modTime time.Time
	size    int64
}

// same reports whether two observations describe the same file content.
// time.Time is compared with Equal rather than ==, which also weighs the
// monotonic reading and the location pointer.
func (c claimHoldStat) same(other claimHoldStat) bool {
	return c.exists == other.exists && c.size == other.size && c.modTime.Equal(other.modTime)
}

// statClaimHoldFile observes the claim-hold file. A missing file is not an
// error: it is the zero claimHoldStat.
func statClaimHoldFile(path string) (claimHoldStat, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return claimHoldStat{}, nil
		}
		return claimHoldStat{}, err
	}
	return claimHoldStat{exists: true, modTime: fi.ModTime(), size: fi.Size()}, nil
}

// claimHoldStore owns the mtime/size bookkeeping that lets the supervisor tell
// a FOREIGN write to claim-hold.json from its own. Every write this process
// makes records the resulting stat, so only someone else's write (or an `rm`)
// is reported as changed. Last writer wins; the file is authoritative.
type claimHoldStore struct {
	path string
	mu   sync.Mutex
	seen claimHoldStat // mtime+size of the last content this process wrote or read
}

// newClaimHoldStore records the file as it stands before hydration, so the
// content the daemon starts from is never mistaken for an external change.
func newClaimHoldStore(path string) *claimHoldStore {
	store := &claimHoldStore{path: path}
	if seen, err := statClaimHoldFile(path); err == nil {
		store.seen = seen
	}
	return store
}

// Write persists the hold and records the stat of what it wrote. A stat that
// fails AFTER a successful write is not an error the operator needs: the write
// landed, and the worst consequence is that the next reload re-adopts this
// process's own value, which is a no-op.
func (s *claimHoldStore) Write(h *supervisor.ClaimHold) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeClaimHoldFile(s.path, h); err != nil {
		return err
	}
	seen, err := statClaimHoldFile(s.path)
	if err != nil {
		slog.Warn("could not stat the claim-hold file after writing it; the next reload may re-adopt this write",
			"path", s.path, "err", err)
		return nil
	}
	s.seen = seen
	return nil
}

// ReloadIfChanged re-reads the claim-hold file when its (mtime, size) differs
// from the last content this process wrote or read. It reports (hold, changed,
// err); a nil hold with changed=true means the file now says "no hold".
func (s *claimHoldStore) ReloadIfChanged() (*supervisor.ClaimHold, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := statClaimHoldFile(s.path)
	if err != nil {
		return nil, false, err
	}
	if current.same(s.seen) {
		return nil, false, nil
	}
	h, err := readClaimHoldFile(s.path)
	if err != nil {
		// Leave `seen` alone so the next tick retries; the supervisor keeps the
		// in-memory hold in the meantime.
		return nil, false, err
	}
	s.seen = current
	return h, true, nil
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
	// Deliberately UNSCOPED. A corrupt record may have named repos we can no
	// longer read, so the fail-safe must be the strictest hold available —
	// never a narrower one synthesized from a guess.
	return &supervisor.ClaimHold{
		Held:      true,
		Actor:     "unknown",
		Reason:    fmt.Sprintf("unreadable %s (fail-safe hold)", claimHoldFileName),
		Since:     now,
		ExpiresAt: now.Add(corruptClaimHoldTTL),
	}
}

// hydrateClaimHold loads the persisted hold into the supervisor and wires both
// the persistence and the reload hooks. Called before the supervisor starts, so
// no agent can cycle past the gate before the hold is in place.
func hydrateClaimHold(d *Daemon, path string) {
	store := newClaimHoldStore(path)
	d.sup.PersistClaimHold = store.Write
	d.sup.ReloadClaimHold = store.ReloadIfChanged
	if h := loadClaimHoldAtStartup(path); h != nil {
		d.sup.LoadClaimHold(h)
		slog.Warn("claim hold restored from disk; the daemon will not start new work",
			"actor", h.Actor, "reason", h.Reason, "expires_at", h.ExpiresAt)
	}
}

// claimHoldStatus builds the payload both socket operations return.
//
// Running is filtered to the hold's scope. A workspace-wide wait under a
// repo-scoped hold could never reach idle: agents on un-held repos keep
// claiming, so every --wait-idle would burn its whole timeout.
func (d *Daemon) claimHoldStatus() ClaimHoldStatus {
	status := ClaimHoldStatus{Hold: d.sup.ClaimHoldSnapshot(), Running: []ClaimHoldRunningAgent{}}
	for _, a := range d.sup.GetAgents() {
		if a.ClaimsGated {
			status.Gated++
		}
		if !holdCoversAgent(status.Hold, a) {
			continue
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

// holdCoversAgent reports whether a running agent falls inside the hold's
// scope, for the quiesce signal Running feeds.
//
// It FAILS SAFE in both directions that matter: no hold or an unscoped one
// covers everything, and a repo-UNBOUND agent is covered too — nothing says
// which repo it is working, so a quiesce must assume it could be the held one.
// On a repo-unbound fleet that degenerates to the current fleet-wide wait,
// which is correct rather than a bug: the scope simply has nothing to bite on.
func holdCoversAgent(h *supervisor.ClaimHold, a supervisor.SupervisedAgentStatus) bool {
	if !h.Scoped() {
		return true
	}
	repos := a.SourceRepos
	if len(repos) == 0 && a.Repo != "" {
		repos = []string{a.Repo}
	}
	if len(repos) == 0 {
		return true
	}
	for _, repo := range repos {
		if h.HoldsRepo(repo) {
			return true
		}
	}
	return false
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

// normalizeHoldRepos canonicalises a requested repo scope and rejects names the
// workspace does not define. Trim, drop blanks, de-duplicate, sort — so the
// persisted record has one shape whatever the caller typed.
//
// A typo'd repo name is an ERROR, not an empty scope: silently accepting
// `--repos flet-db` would hold nothing while telling the operator the quiesce
// is up. Validation is skipped when the daemon knows of no repos at all (a
// non-workspace daemon has no list to check against).
func normalizeHoldRepos(in []string, known []cfgpkg.RepoConfig) ([]string, error) {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		repo := strings.TrimSpace(raw)
		if repo == "" || seen[repo] {
			continue
		}
		seen[repo] = true
		out = append(out, repo)
	}
	if len(out) == 0 {
		return nil, nil
	}
	if len(known) > 0 {
		valid := make(map[string]bool, len(known)*2)
		for _, rc := range known {
			valid[rc.Name] = true
			if rc.SourceRepoID != "" {
				valid[rc.SourceRepoID] = true
			}
		}
		for _, repo := range out {
			if !valid[repo] {
				return nil, fmt.Errorf("unknown repo %q; the workspace defines %s", repo, strings.Join(knownRepoNames(known), ", "))
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// knownRepoNames lists the workspace repo identifiers, for the error above.
func knownRepoNames(known []cfgpkg.RepoConfig) []string {
	names := make([]string, 0, len(known))
	for _, rc := range known {
		if rc.SourceRepoID != "" {
			names = append(names, rc.SourceRepoID)
			continue
		}
		names = append(names, rc.Name)
	}
	sort.Strings(names)
	return names
}

// releaseClaimHold is the Held=false half of claims_hold_set. A foreign holder
// is refused without force by the supervisor, so the error is passed straight
// through rather than reworded here.
func (d *Daemon) releaseClaimHold(args claimHoldSetArgs) DaemonControlResponse {
	if err := d.sup.ReleaseClaimHold(args.Actor, args.Force); err != nil {
		return DaemonControlResponse{Error: err.Error()}
	}
	slog.Info("claim hold released", "actor", args.Actor, "force", args.Force)
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
		return d.releaseClaimHold(args)
	}

	if args.Reason == "" {
		return DaemonControlResponse{Error: "reason is required to hold claims"}
	}
	repos, err := normalizeHoldRepos(args.Repos, d.sup.Repos)
	if err != nil {
		return DaemonControlResponse{Error: err.Error()}
	}
	now := time.Now()
	hold := &supervisor.ClaimHold{Held: true, Actor: args.Actor, Reason: args.Reason, Since: now, Repos: repos}
	if args.TTLSeconds > 0 {
		hold.ExpiresAt = now.Add(time.Duration(args.TTLSeconds) * time.Second)
	}

	if current := d.sup.ClaimHoldSnapshot(); current.Active(now) {
		if current.Actor != args.Actor && !args.Force {
			return DaemonControlResponse{Error: fmt.Sprintf(
				"claims held by %s since %s; use --force to replace", current.Actor, current.Since.Format(time.RFC3339))}
		}
		if current.Actor == args.Actor {
			// Idempotent refresh: only Since is preserved. Reason, TTL and the
			// repo scope all take the new request's values, so re-holding with
			// a different scope narrows or widens the hold rather than being
			// silently ignored.
			hold.Since = current.Since
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
		"expires", hold.ExpiresAt, "repos", supervisor.ClaimHoldScopeLabel(hold),
		"running", len(status.Running))
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
	daemonHoldRepos    []string

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
running agent — the hold gates the claim path only.

By default the hold covers EVERY repo in the workspace and performs no fleet-db
calls, so it works while fleet-db itself is being redeployed. --repos narrows it
to the named repos: agents on other repos keep claiming, which necessarily costs
one ready query per agent cycle to find out what a candidate's repo is. A scoped
hold is therefore NOT safe while fleet-db itself is down — use an unscoped one.

Scope by BLAST RADIUS, not by which repo the deploy names: a deploy that
restarts the supervisors or the issue backend stops every agent in the fleet
however few repos it touches, and must stay unscoped.

The hold survives a daemon restart and applies to THIS daemon (this workspace),
not to the fleet.

Examples:
  loom daemon hold --reason "deploy union tips"
  loom daemon hold --actor union-autodeploy --reason "deploy $SHA" --ttl 45m --wait-idle
  loom daemon hold --reason "quiesce one repo" --repos fleet-db`,
	RunE: runDaemonHold,
}

// daemonReleaseCmd clears the workspace claim hold.
var daemonReleaseCmd = &cobra.Command{
	Use:   "release",
	Short: "Release the workspace claim hold",
	Long: `Clear the workspace claim hold so agents resume claiming work.

Releasing a hold owned by a different actor requires --force.

A hold outlives the daemon, so releasing one does not require a running daemon:
with none alive the claim-hold file is cleared directly, and releasing when
nothing is held succeeds. A daemon that is still starting up is waited for.`,
	RunE: runDaemonRelease,
}

func init() {
	daemonHoldCmd.Flags().StringVar(&daemonHoldReason, "reason", "", "why claims are held (required)")
	daemonHoldCmd.Flags().StringVar(&daemonHoldActor, "actor", "", "who owns the hold (default: $LOOM_ACTOR, else the OS user)")
	daemonHoldCmd.Flags().DurationVar(&daemonHoldTTL, "ttl", defaultClaimHoldTTL, "auto-release after this long (0 = indefinite)")
	daemonHoldCmd.Flags().BoolVar(&daemonHoldWaitIdle, "wait-idle", false, "after setting the hold, wait until no agent is running")
	daemonHoldCmd.Flags().DurationVar(&daemonHoldTimeout, "timeout", defaultClaimHoldWaitLimit, "how long --wait-idle waits before giving up")
	daemonHoldCmd.Flags().BoolVar(&daemonHoldForce, "force", false, "replace a hold owned by a different actor")
	daemonHoldCmd.Flags().StringSliceVar(&daemonHoldRepos, "repos", nil, "limit the hold to these repos (default: all)")

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

// claimHoldEndpoints names the daemon paths a claim-hold command acts on.
// They are resolved TOGETHER, from one project directory or one sidecar
// record, so the file the offline release clears is the file belonging to the
// daemon the socket would have reached — resolving them separately is how a
// release silently clears some other workspace's hold.
type claimHoldEndpoints struct {
	projectDir string
	pidFile    string
	socketPath string
	// holdFile is the daemon's OWN claim-hold path when the sidecar recorded
	// one. Empty means "derive it from pidFile", which is what a cwd-resolved
	// endpoint set has always done.
	holdFile string
	// source names how socketPath was found, for the user-facing lines:
	// controlSocketSourceCwd or controlSocketSourceWorkspaceLock.
	source string
}

// holdPath is the claim-hold record belonging to this daemon: the one the
// sidecar named, or the one beside this daemon's PID file.
func (e claimHoldEndpoints) holdPath() string {
	if e.holdFile != "" {
		return e.holdFile
	}
	return resolveClaimHoldPath(e.pidFile)
}

// describe renders the socket and how it was found, for waiting and error lines.
func (e claimHoldEndpoints) describe() string {
	return describeControlSocket(e.socketPath, e.source)
}

// resolveClaimHoldEndpoints resolves the daemon paths a claim-hold command acts
// on, cwd-first and then from the workspace-lock sidecar. It mirrors
// resolveControlSocketFromCwd's fallback to the default PID file when no daemon
// config can be read.
//
// A hold is an operational lever pulled from wherever the operator or a deploy
// script happens to stand, not necessarily from the daemon's project dir. When
// the cwd has no socket, the whole endpoint set — project dir, socket AND hold
// file — is taken from ONE sidecar, so the file an offline release clears is
// still the file of the daemon the socket would have reached.
func resolveClaimHoldEndpoints() (claimHoldEndpoints, error) {
	projectDir, err := os.Getwd()
	if err != nil {
		return claimHoldEndpoints{}, fmt.Errorf("cannot determine working directory: %w", err)
	}
	config, cfgErr := cfgpkg.LoadDaemonConfig(projectDir)
	if cfgErr != nil {
		config = &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{PIDFile: ".loom/daemon.pid"}}
	}
	pidFile := supervisor.ResolveDaemonPath(projectDir, config.Daemon.PIDFile)
	ep := claimHoldEndpoints{
		projectDir: projectDir,
		pidFile:    pidFile,
		socketPath: filepath.Join(filepath.Dir(pidFile), "daemon.sock"),
		source:     controlSocketSourceCwd,
	}
	if socketExists(ep.socketPath) {
		return ep, nil
	}
	info, ok := workspaceSidecar()
	if !ok || info.Socket == "" {
		// Fall back to the cwd set, so the caller still surfaces the familiar
		// "no control socket at <path>" error and the offline release still
		// works exactly as it did before this change.
		return ep, nil
	}
	ep.socketPath = info.Socket
	ep.source = controlSocketSourceWorkspaceLock
	if info.Cwd != "" {
		ep.projectDir = info.Cwd
		ep.pidFile = supervisor.ResolveDaemonPath(info.Cwd, config.Daemon.PIDFile)
	}
	ep.holdFile = info.ClaimHold // empty for an old sidecar: holdPath() derives it
	return ep, nil
}

// claimHoldDaemonRuntime reports whether a daemon process is believed alive:
// the per-cwd lock/state first, then the workspace-scoped lock for a daemon
// started from a different cwd.
func claimHoldDaemonRuntime(projectDir string) cli.DaemonRuntimeInfo {
	if rt := cli.DetectDaemonRuntime(projectDir); rt.Running {
		return rt
	}
	return detectWorkspaceDaemonRuntime()
}

// dialClaimHoldSocket sends one control request, waiting up to
// claimHoldSocketWait for a daemon that is STARTING to bind its socket. The daemon writes daemon.pid
// before it binds daemon.sock, so a command issued in that window used to fail
// with "daemon is not running" against a daemon that was very much running.
//
// It gives up early in the one case where waiting is pointless: no daemon
// process is alive at all. That is reported as errNoDaemonRunning so a caller
// with an offline fallback can take it, and every other caller can say so.
func dialClaimHoldSocket(ep claimHoldEndpoints, req DaemonControlRequest) (*DaemonControlResponse, error) {
	wait := claimHoldSocketWait
	resp, err := sendDaemonControlRequestFull(ep.socketPath, req)
	if err == nil {
		return resp, nil
	}

	rt := claimHoldDaemonRuntime(ep.projectDir)
	if !rt.Running {
		return nil, errNoDaemonRunning
	}

	// One line, once: the silence here is what made a starting daemon look
	// like a stopped one during triage.
	if rt.PID > 0 {
		fmt.Fprintf(os.Stderr, "Waiting up to %s for the daemon control socket at %s (daemon PID %d is starting)...\n",
			wait, ep.describe(), rt.PID)
	} else {
		fmt.Fprintf(os.Stderr, "Waiting up to %s for the daemon control socket at %s (a daemon is starting)...\n",
			wait, ep.describe())
	}

	deadline := time.Now().Add(wait)
	for {
		time.Sleep(claimHoldSocketPoll)
		if resp, err := sendDaemonControlRequestFull(ep.socketPath, req); err == nil {
			return resp, nil
		}
		if rt.PID > 0 && !lockfile.IsProcessRunning(rt.PID) {
			return nil, errNoDaemonRunning
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf(
				"timed out after %s waiting for the daemon control socket at %s (daemon PID %d is alive but has not bound it); "+
					"%s was deliberately NOT cleared, because that daemon may already hold a hydrated copy",
				wait, ep.describe(), rt.PID, ep.holdPath())
		}
	}
}

// requestClaimHold sends one claims_hold_set / claims_hold_get round trip,
// resolving the daemon paths cwd-first and then from the workspace-lock
// sidecar. Transport failures name the socket and how it was resolved, so a
// wrong-daemon mistake is visible in the error itself.
func requestClaimHold(op string, args any) (*ClaimHoldStatus, error) {
	ep, err := resolveClaimHoldEndpoints()
	if err != nil {
		return nil, err
	}
	return requestClaimHoldAt(ep, op, args)
}

// requestClaimHoldAt is requestClaimHold against already-resolved endpoints, so
// a caller with an offline fallback clears the file those same endpoints name.
func requestClaimHoldAt(ep claimHoldEndpoints, op string, args any) (*ClaimHoldStatus, error) {
	req := DaemonControlRequest{Operation: op}
	if args != nil {
		raw, mErr := json.Marshal(args)
		if mErr != nil {
			return nil, mErr
		}
		req.Args = raw
	}
	resp, err := dialClaimHoldSocket(ep, req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, ep.describe())
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s (%s)", resp.Error, ep.describe())
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

	ep, err := resolveClaimHoldEndpoints()
	if err != nil {
		return err
	}
	status, err := requestClaimHoldAt(ep, ctrlOpClaimHoldSet, claimHoldSetArgs{
		Held:       true,
		Actor:      actor,
		Reason:     daemonHoldReason,
		TTLSeconds: int64(ttl / time.Second),
		Force:      daemonHoldForce,
		Repos:      daemonHoldRepos,
	})
	if err != nil {
		// Deliberately NO offline write path here: a hold written to a file no
		// daemon will read is a quiesce that silently did not happen.
		if errors.Is(err, errNoDaemonRunning) {
			return fmt.Errorf("%w; start the daemon before holding claims (release works without one)", err)
		}
		return err
	}

	printClaimHoldStatus(*status)
	if !daemonHoldWaitIdle {
		return nil
	}
	return waitForClaimHoldIdle(ep, daemonHoldTimeout)
}

// waitForClaimHoldIdle polls claims_hold_get until no agent process is left
// running. On timeout it returns an error WITHOUT releasing the hold — whether
// to give up on a quiesce is the operator's decision, not this command's.
func waitForClaimHoldIdle(ep claimHoldEndpoints, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	fmt.Printf("Waiting up to %s for running agents to finish...\n", timeout)
	fmt.Printf("  Using %s\n", ep.describe())
	for {
		// Poll the SAME endpoints the hold was set on, so every round trip
		// reaches the daemon that is actually holding.
		status, err := requestClaimHoldAt(ep, ctrlOpClaimHoldGet, nil)
		if err != nil {
			return err
		}
		if len(status.Running) == 0 {
			fmt.Println("All agents idle; workspace is quiesced.")
			return nil
		}
		if !time.Now().Before(deadline) {
			fmt.Fprintf(os.Stderr, "Timed out after %s with %d agent(s) still running in repos %s (hold left in place):\n",
				timeout, len(status.Running), supervisor.ClaimHoldScopeLabel(status.Hold))
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
	ep, err := resolveClaimHoldEndpoints()
	if err != nil {
		return err
	}
	status, err := requestClaimHoldAt(ep, ctrlOpClaimHoldSet, claimHoldSetArgs{
		Held:  false,
		Actor: actor,
		Force: daemonReleaseForce,
	})
	if err != nil {
		// A hold outlives the daemon by design, so releasing it must not
		// require one. With no daemon alive the file IS the hold.
		if errors.Is(err, errNoDaemonRunning) {
			return releaseClaimHoldOffline(ep.holdPath(), actor, daemonReleaseForce)
		}
		return err
	}
	fmt.Printf("Claim hold released by %s. Agents resume claiming within ~%s.\n", actor, defaultClaimHoldRecheckHint)
	fmt.Printf("  Released via %s\n", ep.describe())
	printClaimHoldStatus(*status)
	return nil
}

// releaseClaimHoldOffline clears the claim-hold record directly, for the case
// where no daemon is running to be asked. It enforces the SAME ownership rule
// the socket path enforces, with the same wording as
// supervisor.ReleaseClaimHold: a file path that quietly skipped that check
// would let any actor lift someone else's quiesce just by stopping the daemon.
func releaseClaimHoldOffline(path, actor string, force bool) error {
	hold, err := readClaimHoldFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			// The record is 0600; this is another UID, not a missing daemon.
			return fmt.Errorf("cannot read the claim-hold record at %s: permission denied%s",
				path, claimHoldOwnerHint(path))
		}
		// Unreadable or unparseable. Releasable, but only deliberately: the
		// record may describe a hold whose owner cannot be checked.
		if !force {
			return fmt.Errorf("the claim-hold record at %s is unparseable (%v); "+
				"its owner cannot be checked, so use --force to clear it", path, err)
		}
		if wErr := writeClaimHoldFile(path, nil); wErr != nil {
			return fmt.Errorf("clear %s: %w", path, wErr)
		}
		fmt.Printf("Claim hold released by %s (no daemon running; cleared unparseable record at %s).\n", actor, path)
		return nil
	}

	if hold == nil {
		// Idempotent: no file, or a file that says "not held".
		fmt.Println("Claims: not held")
		return nil
	}
	// An expired hold is released by anyone, matching ReleaseClaimHold's
	// !Active fast path.
	if hold.Active(time.Now()) && !force && hold.Actor != actor {
		return fmt.Errorf("claims held by %s since %s; use --force to release",
			hold.Actor, hold.Since.Format(time.RFC3339))
	}
	if err := writeClaimHoldFile(path, nil); err != nil {
		return fmt.Errorf("clear %s: %w", path, err)
	}
	fmt.Printf("Claim hold released by %s (no daemon running; cleared %s).\n", actor, path)
	return nil
}

// claimHoldOwnerHint names the UID that owns an unreadable record, so the
// operator sees "re-run as that user" rather than "daemon is not running". An
// ownership that cannot be determined is omitted rather than guessed.
func claimHoldOwnerHint(path string) string {
	fi, err := os.Lstat(path)
	if err != nil {
		return ""
	}
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf(" (the record is 0600 and owned by UID %d; re-run as that user)", stat.Uid)
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
	return fmt.Sprintf("Claims: %s by %s since %s — %s; repos %s; expires %s",
		marker, h.Actor, h.Since.Format(time.RFC3339), h.Reason,
		supervisor.ClaimHoldScopeLabel(h), expires)
}

// printClaimHoldReleaseHint tells the operator how to clear an active hold and
// which daemon the release would reach. The socket source matters: a release
// run from outside the daemon's project dir resolves through the workspace
// lock, and saying so up front is cheaper than diagnosing it afterwards.
func printClaimHoldReleaseHint(h *supervisor.ClaimHold) {
	if h == nil || !h.Active(time.Now()) {
		return
	}
	fmt.Println("  Release with: loom daemon release")
	if ep, err := resolveClaimHoldEndpoints(); err == nil {
		fmt.Printf("  Release would reach %s\n", ep.describe())
	}
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
	if h.Scoped() {
		fmt.Printf("  Hold applies to THIS daemon (this workspace), and only to repos: %s.\n",
			supervisor.ClaimHoldScopeLabel(h))
		fmt.Println("  A scoped hold still queries fleet-db to learn each candidate's repo,")
		fmt.Println("  so it is NOT safe while fleet-db itself is being redeployed.")
		fmt.Println("  Running agents are untouched; no new work in those repos will be claimed until release.")
		return
	}
	fmt.Println("  Hold applies to THIS daemon (this workspace), not the fleet, and covers all repos.")
	fmt.Println("  Running agents are untouched; no new work will be claimed until release.")
}
