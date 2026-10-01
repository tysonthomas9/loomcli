package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// Config configures the adapter's use of the user's OpenCode service.
type Config struct {
	Bin     string                     // the pinned build (LOOM_OPENCODE_BIN)
	Env     []string                   // the environment of a service Loom starts, and where its registration lives; nil is the user's own (R1)
	Presets []loomharness.PresetConfig // rendered as loom-<name> agents over the user's config
}

// Supervisor timings; variables so tests can shorten them.
var (
	restartBackoff = time.Second      // doubled per consecutive failure
	maxBackoff     = 30 * time.Second // backoff cap
	startTimeout   = time.Minute
	stopGrace      = 10 * time.Second
	exitGrace      = 2 * time.Second // a started service that lost the race exits before the winner registers
)

// maxFailures consecutive failed starts or early exits make Health report
// harness_unavailable.
const maxFailures = 3

// Adapter is the OpenCode harness in shared service mode (Tyson, 2026-10-01
// 17:50-18:00 UTC). It uses whichever `opencode serve --service` is
// registered for the user (R1 roots), his own included, and starts one with
// Loom's filtered environment only when none runs.
//
// The registered service is found the way OpenCode's own clients find it:
// <XDG_STATE_HOME>/opencode/service.json holds its id, version, url, pid and
// password (OpenCode b30c4d0 cli/src/services/service-registration.ts:20-38),
// and it must answer /api/info with that pid (client/src/effect/service.ts:
// 30-48). A service of any version but the pinned one is refused, never
// stopped or replaced. A service's boot sweep resumes interrupted turns from
// the shared database (server/src/process.ts:104-107,
// core/src/session/execution/restart.ts), which is how running turns survive
// a crash.
//
// Loom never stops, restarts or signals a service it did not start. It
// leaves a service it started running when it shuts down (Stop), so the
// user's clients keep using it. The only service it ever signals is one it
// started whose registration still names it, and only on an explicit
// Restart.
type Adapter struct {
	*Client
	cfg Config

	mu       sync.Mutex
	cmd      *exec.Cmd    // a service Loom started; nil once it exits
	reg      registration // the service in use; zero when none
	failures int
	retryAt  time.Time
	stopped  bool
}

// registration is OpenCode's service registration file.
type registration struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	URL      string `json:"url"`
	PID      int    `json:"pid"`
	Password string `json:"password"`
}

var _ loomharness.Harness = (*Adapter)(nil)

// New returns an adapter; nothing starts until the first call.
func New(cfg Config) *Adapter {
	a := &Adapter{Client: NewClient("", ""), cfg: cfg}
	a.ready, a.shellEnv = a.ensure, a.env
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
// reports harness_unavailable after repeated failed connects. It never
// starts a service.
func (a *Adapter) Health(ctx context.Context) (loomharness.Health, error) {
	vc, err := a.version(ctx)
	if err != nil {
		return loomharness.Health{Version: vc, Warning: err.Error()}, nil
	}
	h := loomharness.Health{OK: true, Version: vc, Warning: vc.Warning()}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.reg.PID == 0 && a.failures >= maxFailures {
		h.OK, h.Warning = false, fmt.Sprintf("harness_unavailable: opencode failed %d times in a row", a.failures)
	}
	return h, nil
}

// Restart reconnects to the registered service. Only a service Loom started
// and that is still registered as itself is stopped first; any other is
// never signaled.
func (a *Adapter) Restart(ctx context.Context) error {
	a.mu.Lock()
	if a.cmd != nil && a.reg.PID == a.cmd.Process.Pid {
		if r, ok := a.registered(); ok && r == a.reg {
			stopOwned(a.cmd)
		}
	}
	a.reg, a.retryAt = registration{}, time.Time{}
	a.mu.Unlock()
	return a.ensure(ctx)
}

// Stop disconnects Loom for good (loom serve shutdown). It leaves every
// service running, including one Loom started.
func (a *Adapter) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopped, a.reg = true, registration{}
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

// ensure keeps the Client pointed at the registered service. It is the
// Client's ready hook: while the registration still names the service in
// use it returns at once, else it finds or starts one.
func (a *Adapter) ensure(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return fmt.Errorf("opencode stopped: %w", loomharness.ErrUnavailable)
	}
	if a.reg.PID != 0 {
		if r, ok := a.registered(); ok && r == a.reg && alive(r.PID) {
			return nil
		}
		a.reg = registration{} // the service changed or went away
	}
	if time.Now().Before(a.retryAt) {
		return fmt.Errorf("opencode restart backoff after %d failures: %w", a.failures, loomharness.ErrUnavailable)
	}
	vc, err := a.version(ctx)
	if err != nil {
		return err
	}
	if err := a.connect(ctx, vc.Installed.String()); err != nil {
		a.fail()
		return err
	}
	a.failures = 0
	return nil
}

func (a *Adapter) fail() {
	a.failures++
	a.retryAt = time.Now().Add(min(restartBackoff<<(a.failures-1), maxBackoff))
}

// connect uses the registered service if it runs the pinned version, and
// otherwise starts `opencode serve --service` once and waits for whichever
// service registers, Loom's or a concurrent incumbent. The password is never
// logged.
func (a *Adapter) connect(ctx context.Context, want string) error {
	var exited <-chan struct{} // set while the service Loom started may still exit
	started, gone := false, false
	for deadline := time.Now().Add(startTimeout); ; {
		if r, ok := a.registered(); ok && alive(r.PID) {
			if r.Version != want {
				return fmt.Errorf("opencode service %s (pid %d) is running; Loom needs %s and never stops or replaces a running service: %w",
					r.Version, r.PID, want, loomharness.ErrUnavailable)
			}
			if answers(ctx, r.URL, r.Password, r.PID) {
				a.setEndpoint(r.URL, r.Password)
				a.reg = r
				return nil
			}
		} else if !started {
			var err error
			if exited, err = a.start(); err != nil {
				return err
			}
			started = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			// It failed, or an incumbent won the port; give the incumbent
			// a moment to register.
			exited, gone = nil, true
			if d := time.Now().Add(exitGrace); d.Before(deadline) {
				deadline = d
			}
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			if gone {
				return fmt.Errorf("opencode serve --service exited during start and no service registered: %w", loomharness.ErrUnavailable)
			}
			return fmt.Errorf("no OpenCode service answered within %s: %w", startTimeout, loomharness.ErrUnavailable)
		}
	}
}

// start launches `opencode serve --service` with Loom's filtered environment
// in its own session, so it outlives Loom. Without --port or --hostname it
// binds where the user's own service would (server-process.ts:61-62). At
// listen time it writes the registration and, only if absent, a password
// into <XDG_CONFIG_HOME>/opencode/service.json (server-process.ts:127-139).
func (a *Adapter) start() (<-chan struct{}, error) {
	env, err := a.env()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(a.cfg.Bin, "serve", "--service") //nolint:gosec // G204: the configured OpenCode binary.
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("opencode serve --service: %w: %w", loomharness.ErrUnavailable, err)
	}
	a.cmd = cmd
	exited := make(chan struct{})
	go a.watch(cmd, exited)
	return exited, nil
}

// watch reaps a service Loom started. When it was the service in use, the
// next call finds or starts one; after the backoff, ensure runs once by
// itself so a service boots and resumes interrupted turns without waiting
// for a Loom call.
func (a *Adapter) watch(cmd *exec.Cmd, exited chan struct{}) {
	_ = cmd.Wait()
	close(exited)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd == cmd {
		a.cmd = nil
	}
	if a.reg.PID != cmd.Process.Pid || a.stopped {
		return
	}
	a.reg = registration{}
	a.fail()
	time.AfterFunc(time.Until(a.retryAt), func() { _ = a.ensure(context.Background()) })
}

// stopOwned stops a service Loom started: SIGTERM, then SIGKILL after
// stopGrace. A graceful stop keeps its turns' execution claims, so the next
// service's boot sweep resumes them (restart.ts:36-41).
func stopOwned(cmd *exec.Cmd) {
	_ = cmd.Process.Signal(syscall.SIGTERM)
	for deadline := time.Now().Add(stopGrace); alive(cmd.Process.Pid); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return
		}
	}
}

// registered reads the user's service registration.
func (a *Adapter) registered() (registration, bool) {
	var r registration
	b, err := os.ReadFile(a.registrationFile())
	if err != nil || json.Unmarshal(b, &r) != nil || r.PID <= 0 || r.URL == "" {
		return registration{}, false
	}
	return r, true
}

// registrationFile is <XDG_STATE_HOME or HOME/.local/state>/opencode/
// service.json of the environment Loom runs OpenCode with
// (util/src/global-roots.ts:8, service-config.ts:29-32).
func (a *Adapter) registrationFile() string {
	env := a.cfg.Env
	if env == nil {
		env = os.Environ()
	}
	get := func(k string) string {
		v := ""
		for _, kv := range env {
			if name, val, _ := strings.Cut(kv, "="); name == k {
				v = val
			}
		}
		return v
	}
	state := get("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(get("HOME"), ".local", "state")
	}
	return filepath.Join(state, "opencode", "service.json")
}

// alive reports whether pid runs; EPERM means it runs as another user.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// githubTokens never reach a service Loom starts, nor the processes it
// starts: agents publish through Loom (R32). Inherited OpenCode passwords
// are dropped too; a service reads its own from its config, and sessions get
// env without either (see Session.isolate).
var githubTokens = []string{"GITHUB_TOKEN", "GH_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_TOKEN_FILE", "LOOM_PR_GIT_PASSWORD"}

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

// answers reports whether the server at base is up and is process pid.
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
