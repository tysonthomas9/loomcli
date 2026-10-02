package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	Presets []loomharness.PresetConfig // written as loom-<name> agents under Worktrees
	// Worktrees is the root above every agent worktree (agentworktree's
	// <root>/<repo>/<key>). Loom keeps its presets in Worktrees/.opencode/agent,
	// where every OpenCode service finds them for sessions below it.
	Worktrees string
	// Bridge is the `loom agent mcp-bridge` command. It is registered once as
	// the "loom" MCP server in Worktrees/.opencode/opencode.json; OpenCode
	// starts it in each session's directory, where it finds that agent's
	// settings (bridgeEnv). nil registers none.
	Bridge []string
	// BridgeFile names where the settings of the agent whose worktree is dir
	// go (agentmcp.EnvFile); nil refuses an agent with settings.
	BridgeFile func(dir string) (string, error)
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
	synced   bool      // the preset files match cfg.Presets
	cmd      *exec.Cmd // a service Loom started; nil once it exits
	exited   <-chan struct{}
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
	if cfg.Worktrees != "" {
		a.presets = filepath.Clean(cfg.Worktrees)
	}
	a.defined, a.bridgeFile = a.definesPreset, cfg.BridgeFile
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
	if !a.synced {
		if err := a.syncPresets(); err != nil {
			return err
		}
		a.synced = true
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
	if _, err := a.version(ctx); err != nil {
		return err // missing, or below the minimum (fail closed)
	}
	if err := a.connect(ctx); err != nil {
		if ctx.Err() == nil { // a caller's cancel is not a service failure
			a.fail()
		}
		return err
	}
	a.failures = 0
	return nil
}

func (a *Adapter) fail() {
	a.failures++
	a.retryAt = time.Now().Add(min(restartBackoff<<(a.failures-1), maxBackoff))
}

// connect uses the registered service if it runs at least the minimum
// version (R22; Tyson 18:41 UTC: newer is fine), and
// otherwise starts `opencode serve --service` once and waits for whichever
// service registers, Loom's or a concurrent incumbent. The password is never
// logged.
func (a *Adapter) connect(ctx context.Context) error {
	minimum := loomharness.Versions["opencode"].Minimum
	// exited is set while a service Loom started may still exit, including
	// one an earlier, cancelled call started: Loom never starts a second
	// service while its first is still coming up.
	exited, started, gone := a.exited, a.cmd != nil, false
	if started {
		select {
		case <-exited: // it already exited (a crash or Restart); start afresh
			exited, started = nil, false
		default:
		}
	}
	for deadline := time.Now().Add(startTimeout); ; {
		if r, ok := a.registered(); ok && alive(r.PID) {
			if v, err := loomharness.ParseVersion(r.Version); err != nil || v.Less(minimum) {
				return fmt.Errorf("opencode service %s (pid %d) is running; Loom needs %s or newer and never stops or replaces a running service: %w",
					r.Version, r.PID, minimum, loomharness.ErrUnavailable)
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
	exited := make(chan struct{})
	a.cmd, a.exited = cmd, exited
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
		a.cmd, a.exited = nil, nil
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

// projectConfigOff are OpenCode's switches that stop it reading project
// .opencode directories, where Loom's presets live (server-process.ts:108-110).
var projectConfigOff = []string{"OPENCODE_DISABLE_PROJECT_CONFIG", "OPENCODE_CONFIG_PROJECT_DISABLE"}

// env is the configured environment without GitHub tokens, OpenCode
// passwords or the project-config switches.
func (a *Adapter) env() ([]string, error) {
	env := a.cfg.Env
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if slices.Contains(githubTokens, k) || slices.Contains(projectConfigOff, k) || k == "OPENCODE_SERVER_PASSWORD" || k == "OPENCODE_PASSWORD" {
			continue
		}
		out = append(out, kv)
	}
	return out, nil
}

// SetPresets replaces Loom's presets and rewrites their files; running
// services reload them without a restart.
func (a *Adapter) SetPresets(presets []loomharness.PresetConfig) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Presets = slices.Clone(presets)
	a.synced = false
	if err := a.syncPresets(); err != nil {
		return err
	}
	a.synced = true
	return nil
}

// definesPreset reports whether agent is loom-<name> for a current preset.
// A removed preset's file can still be listed until the service reloads.
func (a *Adapter) definesPreset(agent string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.ContainsFunc(a.cfg.Presets, func(p loomharness.PresetConfig) bool { return "loom-"+p.Name == agent })
}

// presetName is what a preset name may be: it becomes a file name.
var presetName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// syncPresets makes <Worktrees>/.opencode/agent hold exactly one
// loom-<name>.md per preset (frontmatter mode primary, body the persona;
// config/plugin/agent.ts:98-120) and no other loom-*.md. Other files in the
// directory are never touched. Files are replaced atomically and only when
// their content changes, so services reload only on a real change. With a
// Bridge, <Worktrees>/.opencode/opencode.json registers it as MCP server "loom".
func (a *Adapter) syncPresets() error {
	if len(a.cfg.Presets) == 0 && a.presets == "" {
		return nil
	}
	if a.presets == "" {
		return fmt.Errorf("opencode presets: no worktrees root configured: %w", loomharness.ErrUnavailable)
	}
	dir := filepath.Join(a.presets, ".opencode", "agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("opencode presets: %w", err)
	}
	want := map[string][]byte{}
	for _, p := range a.cfg.Presets {
		if !presetName.MatchString(p.Name) {
			return fmt.Errorf("opencode presets: invalid preset name %q", p.Name)
		}
		want["loom-"+p.Name+".md"] = []byte("---\ndescription: Loom preset " + p.Name + "\nmode: primary\n---\n" + p.Persona + "\n")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("opencode presets: %w", err)
	}
	for _, e := range entries {
		if name := e.Name(); strings.HasPrefix(name, "loom-") && strings.HasSuffix(name, ".md") && want[name] == nil && e.Type().IsRegular() {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return fmt.Errorf("opencode presets: %w", err)
			}
		}
	}
	for name, content := range want {
		if err := replaceFile(filepath.Join(dir, name), content); err != nil {
			return fmt.Errorf("opencode presets: %w", err)
		}
	}
	if len(a.cfg.Bridge) == 0 {
		return nil
	}
	cfg, err := json.Marshal(map[string]any{"mcp": map[string]any{"loom": map[string]any{
		"type": "local", "command": a.cfg.Bridge}}})
	if err == nil {
		err = replaceFile(filepath.Join(a.presets, ".opencode", "opencode.json"), cfg)
	}
	if err != nil {
		return fmt.Errorf("opencode bridge: %w", err)
	}
	return nil
}

// replaceFile atomically replaces file with content (mode 0600), only when
// its content changes, so services reload only on a real change.
func replaceFile(file string, content []byte) error {
	if old, err := os.ReadFile(file); err == nil && bytes.Equal(old, content) { //nolint:gosec // G304: Loom's own file under the configured worktrees root.
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".loom-preset-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(content)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), file); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// bridgeEnv writes the agent's bridge settings (its Launch.Env, with its
// token) where the bridge OpenCode starts in dir reads them, mode 0600.
func (c *Client) bridgeEnv(dir string, env map[string]string) error {
	if c.bridgeFile == nil {
		return errors.New("no bridge settings file configured")
	}
	file, err := c.bridgeFile(dir)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return replaceFile(file, raw)
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
