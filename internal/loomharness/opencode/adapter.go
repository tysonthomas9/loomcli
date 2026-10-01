package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	startTimeout   = time.Minute
	stopGrace      = 10 * time.Second
)

// maxFailures consecutive failed starts or early exits make Health report
// harness_unavailable.
const maxFailures = 3

// Adapter is the OpenCode harness: it supervises one `opencode serve
// --service` on a free loopback port and serves the port through Client. The
// server starts on first use, restarts with backoff when it exits, and is
// stopped only by Stop or Restart, which signal the owned process only.
type Adapter struct {
	*Client
	cfg Config

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
	a := &Adapter{Client: NewClient("", ""), cfg: cfg}
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

// spawn starts `opencode serve --service` and waits until it answers with
// its own pid. In service mode OpenCode picks the password itself (random per
// boot unless the user configured one) and records it in service.json.
func (a *Adapter) spawn(ctx context.Context) error {
	env, err := a.env()
	if err != nil {
		return err
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("opencode: free port: %w: %w", loomharness.ErrUnavailable, err)
	}
	base := "http://" + l.Addr().String()
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()
	cmd := exec.Command(a.cfg.Bin, "serve", "--service", "--hostname", "127.0.0.1", "--port", port) //nolint:gosec // G204: the configured OpenCode binary.
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("opencode serve: %w: %w", loomharness.ErrUnavailable, err)
	}
	exited := make(chan struct{})
	started := time.Now()
	go a.watch(cmd, exited, started)
	kill := func(err error) error {
		_ = cmd.Process.Kill()
		<-exited
		return err
	}
	file := filepath.Join(stateDir(env), "opencode", "service.json")
	for deadline := started.Add(startTimeout); ; {
		select {
		case <-exited:
			return fmt.Errorf("opencode serve exited during start (another OpenCode service may own %s): %w", file, loomharness.ErrUnavailable)
		case <-ctx.Done():
			return kill(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
		if pw, ok := servicePassword(file, cmd.Process.Pid); ok && answers(ctx, base, pw, cmd.Process.Pid) {
			a.setEndpoint(base, pw)
			a.cmd, a.exited = cmd, exited
			return nil
		}
		if time.Now().After(deadline) {
			return kill(fmt.Errorf("opencode serve not ready after %s: %w", startTimeout, loomharness.ErrUnavailable))
		}
	}
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

// stopLocked sends SIGTERM to the owned server, waits, then SIGKILLs it.
func (a *Adapter) stopLocked() {
	cmd, exited := a.cmd, a.exited
	if cmd == nil {
		return
	}
	a.cmd = nil
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(stopGrace):
		_ = cmd.Process.Kill()
		<-exited
	}
}

// env is the configured environment plus the Loom presets merged into
// OPENCODE_CONFIG_CONTENT, which OpenCode applies over the user's config.
func (a *Adapter) env() ([]string, error) {
	env := a.cfg.Env
	if env == nil {
		env = os.Environ()
	}
	if len(a.cfg.Presets) == 0 {
		return env, nil
	}
	content := map[string]any{}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "OPENCODE_CONFIG_CONTENT="); ok {
			if err := json.Unmarshal([]byte(v), &content); err != nil {
				return nil, fmt.Errorf("opencode: merge presets into OPENCODE_CONFIG_CONTENT: %w", err)
			}
			continue
		}
		out = append(out, kv)
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

// stateDir is OpenCode's XDG state root for env.
func stateDir(env []string) string {
	var home, state string // the last value wins, as in exec.Cmd
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "XDG_STATE_HOME="); ok {
			state = v
		}
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = v
		}
	}
	if state != "" {
		return state
	}
	return filepath.Join(home, ".local", "state")
}

// servicePassword reads the password a service-mode server with this pid
// recorded.
func servicePassword(file string, pid int) (string, bool) {
	b, err := os.ReadFile(file) //nolint:gosec // G304: OpenCode's own service registration file.
	if err != nil {
		return "", false
	}
	var s struct {
		PID      int    `json:"pid"`
		Password string `json:"password"`
	}
	if json.Unmarshal(b, &s) != nil || s.PID != pid || s.Password == "" {
		return "", false
	}
	return s.Password, true
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
