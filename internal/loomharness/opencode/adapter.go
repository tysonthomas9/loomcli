package opencode

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// Config configures the one supervised OpenCode server.
type Config struct {
	Bin     string                     // the pinned build (LOOM_OPENCODE_BIN)
	Env     []string                   // the server's environment; nil is the user's own (R1)
	Presets []loomharness.PresetConfig // rendered as loom-<name> agents over the user's config
}

// Supervisor timings; variables so tests can shorten them.
var (
	restartBackoff = time.Second      // doubled per consecutive failure
	maxBackoff     = 30 * time.Second // backoff cap
	stableAfter    = time.Minute      // an exit after this long is a fresh failure
	trackEvery     = time.Second      // how often the server's descendants are recorded
	startTimeout   = time.Minute
	stopGrace      = 10 * time.Second
)

// maxFailures consecutive failed starts or early exits make Health report
// harness_unavailable.
const maxFailures = 3

// Adapter is the OpenCode harness: it supervises one plain `opencode serve`
// on a free loopback port with a random per-boot password (R-D) and serves
// the port through Client. It never uses --service, so the user's own
// OpenCode service registration and config are never touched. The
// server starts on first use, restarts with backoff when it exits, and is
// stopped only by Loom's own Stop or Restart calls.
//
// Loom owns the process tree it starts. The server runs in its own process
// group, but OpenCode starts shell commands and its PTY daemon detached, so
// they leave that group and outlive a crashed server. While the server runs,
// track records its descendants (by parent PID) with their start times; reap
// signals exactly the recorded processes that still have the same start
// time, plus their current descendants. It never pattern-kills and never
// touches a process outside that tree, such as the user's own OpenCode.
type Adapter struct {
	*Client
	cfg Config

	treeMu sync.Mutex
	tree   map[int]string // recorded descendant PID -> start time

	mu       sync.Mutex
	cmd      *exec.Cmd // the owned server; nil when none runs
	exited   chan struct{}
	failures int
	retryAt  time.Time
	stopped  bool
}

var _ loomharness.Harness = (*Adapter)(nil)

// New returns an adapter; nothing starts until the first call.
func New(cfg Config) *Adapter {
	a := &Adapter{Client: NewClient("", ""), cfg: cfg, tree: map[int]string{}}
	a.ready = a.ensure
	return a
}

// Name is the harness name.
func (a *Adapter) Name() string { return "opencode" }

// Session returns the port session for one recorded ref.
func (a *Adapter) Session(ref loomharness.NativeRef) loomharness.Session {
	return a.Client.Session(ref)
}

// Models lists the models the server offers as "provider/model" ids.
func (a *Adapter) Models(ctx context.Context) ([]loomharness.Model, error) {
	var r struct {
		Data []struct {
			ID         string `json:"id"`
			ProviderID string `json:"providerID"`
			Name       string `json:"name"`
		} `json:"data"`
	}
	if err := a.call(ctx, "GET", "/api/model", nil, &r); err != nil {
		return nil, err
	}
	out := make([]loomharness.Model, len(r.Data))
	for i, m := range r.Data {
		out[i] = loomharness.Model{ID: m.ProviderID + "/" + m.ID, Name: m.Name}
	}
	return out, nil
}

// Health checks the installed version (refused below the minimum) and
// reports harness_unavailable after repeated failed starts. It never starts
// the server.
func (a *Adapter) Health(ctx context.Context) (loomharness.Health, error) {
	vc, err := a.version(ctx)
	if err != nil {
		return loomharness.Health{Version: vc, Warning: err.Error()}, nil
	}
	h := loomharness.Health{OK: true, Version: vc, Warning: vc.Warning()}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd == nil && a.failures >= maxFailures {
		h.OK, h.Warning = false, fmt.Sprintf("harness_unavailable: opencode failed %d times in a row", a.failures)
	}
	return h, nil
}

// Restart stops the owned server and starts a new one.
func (a *Adapter) Restart(ctx context.Context) error {
	a.mu.Lock()
	a.stopLocked()
	a.retryAt = time.Time{}
	a.mu.Unlock()
	return a.ensure(ctx)
}

// Stop stops the owned server for good (loom serve shutdown).
func (a *Adapter) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopped = true
	a.stopLocked()
	a.reap() // also after a crash, when no server runs
}

func (a *Adapter) version(ctx context.Context) (loomharness.VersionCheck, error) {
	cmd := exec.CommandContext(ctx, a.cfg.Bin, "--version") //nolint:gosec // G204: the configured OpenCode binary.
	cmd.Env = a.cfg.Env
	out, err := cmd.Output()
	if err != nil {
		return loomharness.VersionCheck{}, fmt.Errorf("opencode --version: %w: %w", loomharness.ErrUnavailable, err)
	}
	return loomharness.CheckVersion("opencode", string(out))
}

// ensure starts the server if none runs. It is the Client's ready hook.
func (a *Adapter) ensure(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case a.cmd != nil:
		return nil
	case a.stopped:
		return fmt.Errorf("opencode stopped: %w", loomharness.ErrUnavailable)
	case time.Now().Before(a.retryAt):
		return fmt.Errorf("opencode restart backoff after %d failures: %w", a.failures, loomharness.ErrUnavailable)
	}
	if _, err := a.version(ctx); err != nil {
		return err
	}
	if err := a.spawn(ctx); err != nil {
		a.fail()
		return err
	}
	return nil
}

func (a *Adapter) fail() {
	a.failures++
	a.retryAt = time.Now().Add(min(restartBackoff<<(a.failures-1), maxBackoff))
}

// spawn starts `opencode serve` with a fresh password and waits until it
// answers with its own pid. The password is never logged.
func (a *Adapter) spawn(ctx context.Context) error {
	env, err := a.env()
	if err != nil {
		return err
	}
	password := newPassword()
	env = append(env, "OPENCODE_SERVER_PASSWORD="+password)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("opencode: free port: %w: %w", loomharness.ErrUnavailable, err)
	}
	base := "http://" + l.Addr().String()
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()
	cmd := exec.Command(a.cfg.Bin, "serve", "--hostname", "127.0.0.1", "--port", port) //nolint:gosec // G204: the configured OpenCode binary.
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	a.reap() // leftovers of a crashed server: no duplicate survivor
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("opencode serve: %w: %w", loomharness.ErrUnavailable, err)
	}
	exited := make(chan struct{})
	started := time.Now()
	go a.watch(cmd, exited, started)
	go a.track(cmd.Process.Pid, exited)
	kill := func(err error) error {
		a.record(cmd.Process.Pid)
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-exited
		a.reap()
		return err
	}
	for deadline := started.Add(startTimeout); ; {
		select {
		case <-exited:
			a.reap()
			return fmt.Errorf("opencode serve exited during start: %w", loomharness.ErrUnavailable)
		case <-ctx.Done():
			return kill(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
		if answers(ctx, base, password, cmd.Process.Pid) {
			a.setEndpoint(base, password)
			a.cmd, a.exited = cmd, exited
			a.record(cmd.Process.Pid)
			return nil
		}
		if time.Now().After(deadline) {
			return kill(fmt.Errorf("opencode serve not ready after %s: %w", startTimeout, loomharness.ErrUnavailable))
		}
	}
}

// newPassword is a random per-boot server password.
func newPassword() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (Go 1.24+)
	return base64.RawURLEncoding.EncodeToString(b)
}

// watch reaps the server and, when it exits on its own, restarts it after
// the backoff.
func (a *Adapter) watch(cmd *exec.Cmd, exited chan struct{}, started time.Time) {
	_ = cmd.Wait()
	close(exited)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd != cmd {
		return // stopped on purpose, or a start that failed
	}
	a.cmd = nil
	if time.Since(started) >= stableAfter {
		a.failures = 0
	}
	a.fail()
	time.AfterFunc(time.Until(a.retryAt), func() { _ = a.ensure(context.Background()) })
}

// stopLocked sends SIGTERM to the owned server's process group, waits, then
// SIGKILLs it, and reaps the detached rest of the tree.
func (a *Adapter) stopLocked() {
	cmd, exited := a.cmd, a.exited
	if cmd == nil {
		return
	}
	a.cmd = nil
	a.record(cmd.Process.Pid)
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(stopGrace):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-exited
	}
	a.reap()
}

// track records the server's descendants until it exits.
func (a *Adapter) track(server int, exited <-chan struct{}) {
	for {
		a.record(server)
		select {
		case <-exited:
			return
		case <-time.After(trackEvery):
		}
	}
}

// record adds the current descendants of server to the recorded tree.
func (a *Adapter) record(server int) {
	procs := processes()
	a.treeMu.Lock()
	defer a.treeMu.Unlock()
	for _, pid := range descendants(procs, []int{server}) {
		a.tree[pid] = procs[pid].start
	}
}

// owned lists the recorded processes that still run with the same start
// time, plus their current descendants, and forgets the rest.
func (a *Adapter) owned() []int {
	procs := processes()
	a.treeMu.Lock()
	defer a.treeMu.Unlock()
	var roots []int
	for pid, start := range a.tree {
		if p, ok := procs[pid]; ok && p.start == start {
			roots = append(roots, pid)
		} else {
			delete(a.tree, pid)
		}
	}
	return append(roots, descendants(procs, roots)...)
}

// reap stops the owned processes: SIGTERM, then SIGKILL after stopGrace. It
// lists them again before each signal, so an exited, reused PID is never hit.
func (a *Adapter) reap() {
	sig := syscall.SIGTERM
	for deadline := time.Now().Add(stopGrace); ; time.Sleep(50 * time.Millisecond) {
		pids := a.owned()
		if len(pids) == 0 {
			return
		}
		if time.Now().After(deadline) {
			sig = syscall.SIGKILL
		}
		for _, pid := range pids {
			_ = syscall.Kill(pid, sig)
		}
		if sig == syscall.SIGKILL {
			return
		}
	}
}

type process struct {
	ppid  int
	start string
}

// processes is the ps table: PID -> parent and start time. Zombies are left
// out; they are already dead.
func processes() map[int]process {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,stat=,lstart=").Output()
	if err != nil {
		return nil
	}
	procs := map[int]process{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || strings.HasPrefix(f[2], "Z") {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			procs[pid] = process{ppid: ppid, start: strings.Join(f[3:], " ")}
		}
	}
	return procs
}

// descendants lists every process below roots in procs.
func descendants(procs map[int]process, roots []int) []int {
	kids := map[int][]int{}
	for pid, p := range procs {
		kids[p.ppid] = append(kids[p.ppid], pid)
	}
	var out []int
	for queue := slices.Clone(roots); len(queue) > 0; {
		pid := queue[0]
		queue = queue[1:]
		for _, k := range kids[pid] {
			if k != os.Getpid() {
				out = append(out, k)
				queue = append(queue, k)
			}
		}
	}
	return out
}

// githubTokens never reach the server, nor the processes it starts: agents
// publish through Loom (R32). Inherited OpenCode passwords are dropped too;
// spawn sets its own.
var githubTokens = []string{"GITHUB_TOKEN", "GH_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_TOKEN_FILE"}

// env is the configured environment without GitHub tokens, plus the Loom
// presets merged into OPENCODE_CONFIG_CONTENT, which OpenCode applies over the
// user's config.
func (a *Adapter) env() ([]string, error) {
	env := a.cfg.Env
	if env == nil {
		env = os.Environ()
	}
	content := map[string]any{}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case slices.Contains(githubTokens, k), k == "OPENCODE_SERVER_PASSWORD", k == "OPENCODE_PASSWORD":
		case k == "OPENCODE_CONFIG_CONTENT" && len(a.cfg.Presets) > 0:
			if err := json.Unmarshal([]byte(v), &content); err != nil {
				return nil, fmt.Errorf("opencode: merge presets into OPENCODE_CONFIG_CONTENT: %w", err)
			}
		default:
			out = append(out, kv)
		}
	}
	if len(a.cfg.Presets) == 0 {
		return out, nil
	}
	agents, _ := content["agents"].(map[string]any)
	if agents == nil {
		agents = map[string]any{}
	}
	for _, p := range a.cfg.Presets {
		agents["loom-"+p.Name] = map[string]any{"system": p.Persona, "mode": "primary"}
	}
	content["agents"] = agents
	b, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	return append(out, "OPENCODE_CONFIG_CONTENT="+string(b)), nil
}

// answers reports whether the server at base is up and is our process.
func answers(ctx context.Context, base, password string, pid int) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/api/info", nil)
	if err != nil {
		return false
	}
	req.SetBasicAuth("opencode", password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var info struct {
		PID int `json:"pid"`
	}
	return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&info) == nil && info.PID == pid
}
