package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/codex/protocol"
	"github.com/tysonthomas9/loomcli/internal/loomharness/proctree"
)

// Config configures the supervised app-servers.
type Config struct {
	Bin string   // the user's installed codex; "" is `codex` on PATH
	Env []string // loom serve's environment; nil is os.Environ()
	// Unrouted receives each root's messages for threads with no route, and
	// a Gap when that root's connection ends. It runs on the reader.
	Unrouted func(root string, m Message)
}

// Supervisor timings; variables so tests can shorten them.
var (
	restartBackoff = time.Second      // doubled per consecutive failure
	maxBackoff     = 30 * time.Second // backoff cap
	stableAfter    = time.Minute      // an exit after this long is a fresh failure
	startTimeout   = time.Minute
	stopGrace      = 10 * time.Second
	trackEvery     = time.Second // how often a server's descendants are recorded
)

// maxFailures consecutive failed starts or early exits of one root's server
// make Health report harness_unavailable.
const maxFailures = 3

// githubTokens never reach an app-server, nor the tools it runs: agents
// publish through Loom (R32). Host Publish and review_post keep their own.
var githubTokens = []string{"GITHUB_TOKEN", "GH_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_TOKEN_FILE", "LOOM_PR_GIT_PASSWORD"}

// Supervisor runs one `codex app-server` per distinct canonical codex root:
// every agent on the user's inherited root shares one, and each profile root
// gets its own. Each runs on the user's own login and config; only a profile
// root sets CODEX_HOME. A server starts on first use, and again after an exit
// (with backoff) on the next use; its exit is a gap only for its own threads.
// Each server's process tree is recorded while it runs and reaped when it
// stops or crashes, including tool processes that left its process group
// (proctree); another root's tree is never touched.
type Supervisor struct {
	cfg     Config
	mu      sync.Mutex
	servers map[string]*server // by canonical root
	stopped bool
}

type server struct {
	mu       sync.Mutex // held while starting or stopping
	conn     *Conn
	group    *proctree.Group // the app-server and its process group
	exited   chan struct{}   // closed once the server exited and its tree was reaped
	tree     *proctree.Tree
	failures int
	retryAt  time.Time
}

// New returns a supervisor; nothing starts until the first Conn.
func New(cfg Config) *Supervisor {
	if cfg.Bin == "" {
		cfg.Bin = "codex"
	}
	return &Supervisor{cfg: cfg, servers: map[string]*server{}}
}

// Root is the canonical form of a codex root; "" is the inherited root.
func (s *Supervisor) Root(root string) string {
	if root == "" {
		root = s.getenv("CODEX_HOME")
		if root == "" {
			root = filepath.Join(s.getenv("HOME"), ".codex")
		}
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	// Resolve symlinks in the longest existing prefix, so a root names the
	// same server before and after its directory is created.
	for dir, rest := filepath.Clean(root), ""; ; {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Clean(root)
		}
		dir, rest = parent, filepath.Join(filepath.Base(dir), rest)
	}
}

// Conn returns the connection to root's app-server, starting it if none runs.
func (s *Supervisor) Conn(ctx context.Context, root string) (*Conn, error) {
	root = s.Root(root)
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil, fmt.Errorf("codex stopped: %w", loomharness.ErrUnavailable)
	}
	srv := s.servers[root]
	if srv == nil {
		srv = &server{tree: proctree.New()}
		s.servers[root] = srv
	}
	s.mu.Unlock()

	srv.mu.Lock()
	defer srv.mu.Unlock()
	switch {
	case srv.conn != nil && srv.conn.Err() == nil:
		return srv.conn, nil
	case srv.conn != nil: // exited; watch is reaping it
		return nil, fmt.Errorf("codex app-server for %s exited: %w", root, loomharness.ErrUnavailable)
	case time.Now().Before(srv.retryAt):
		return nil, fmt.Errorf("codex app-server for %s: restart backoff after %d failures: %w", root, srv.failures, loomharness.ErrUnavailable)
	}
	if err := s.start(ctx, root, srv); err != nil {
		var old *loomharness.TooOldError
		if !errors.As(err, &old) {
			srv.fail()
		}
		return nil, err
	}
	return srv.conn, nil
}

// Health checks the installed version (refused below the minimum, a warning
// above the tested one) and reports harness_unavailable when a root's server
// failed repeatedly. It starts nothing.
func (s *Supervisor) Health(ctx context.Context) (loomharness.Health, error) {
	vc, err := s.version(ctx, s.Root(""))
	if err != nil {
		return loomharness.Health{Version: vc, Warning: err.Error()}, nil
	}
	h := loomharness.Health{OK: true, Version: vc, Warning: vc.Warning()}
	s.mu.Lock()
	defer s.mu.Unlock()
	for root, srv := range s.servers {
		srv.mu.Lock()
		if srv.conn == nil && srv.failures >= maxFailures {
			h.OK, h.Warning = false, fmt.Sprintf("harness_unavailable: codex app-server for %s failed %d times in a row", root, srv.failures)
		}
		srv.mu.Unlock()
	}
	return h, nil
}

// Restart stops root's app-server and starts a new one; other roots keep running.
func (s *Supervisor) Restart(ctx context.Context, root string) error {
	root = s.Root(root)
	s.mu.Lock()
	srv := s.servers[root]
	s.mu.Unlock()
	if srv != nil {
		srv.mu.Lock()
		srv.stop()
		srv.retryAt = time.Time{}
		srv.mu.Unlock()
	}
	_, err := s.Conn(ctx, root)
	return err
}

// running lists the roots whose app-server runs.
func (s *Supervisor) running() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var roots []string
	for root, srv := range s.servers {
		srv.mu.Lock()
		if srv.conn != nil {
			roots = append(roots, root)
		}
		srv.mu.Unlock()
	}
	return roots
}

// Stop stops every app-server for good (loom serve shutdown).
func (s *Supervisor) Stop() {
	s.mu.Lock()
	s.stopped = true
	servers := s.servers
	s.mu.Unlock()
	for _, srv := range servers {
		srv.mu.Lock()
		srv.stop()
		srv.mu.Unlock()
	}
}

func (srv *server) fail() {
	srv.failures++
	srv.retryAt = time.Now().Add(min(restartBackoff<<(srv.failures-1), maxBackoff))
}

// version runs `codex --version` in root's environment and applies the gate.
func (s *Supervisor) version(ctx context.Context, root string) (loomharness.VersionCheck, error) {
	cmd := exec.CommandContext(ctx, s.cfg.Bin, "--version") //nolint:gosec // G204: the configured codex binary.
	cmd.Env = s.env(root)
	out, err := cmd.Output()
	if err != nil {
		return loomharness.VersionCheck{}, fmt.Errorf("codex --version: %w: %w", loomharness.ErrUnavailable, err)
	}
	return loomharness.CheckVersion("codex", string(out))
}

// start checks the version, spawns root's app-server in its own process
// group and initializes it, checking it serves root.
func (s *Supervisor) start(ctx context.Context, root string, srv *server) error {
	if _, err := s.version(ctx, root); err != nil {
		return err // too old: refused before any spawn
	}
	cmd := exec.Command(s.cfg.Bin, "app-server") //nolint:gosec // G204: the configured codex binary.
	cmd.Env = s.env(root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	inW, outR, err := startPiped(cmd)
	if err != nil {
		return fmt.Errorf("codex app-server: %w: %w", loomharness.ErrUnavailable, err)
	}
	var fallback Handler
	if s.cfg.Unrouted != nil {
		fallback = func(m Message) { s.cfg.Unrouted(root, m) }
	}
	group := proctree.NewGroup(cmd)
	conn, exited, started := NewConn(outR, inW, fallback), make(chan struct{}), time.Now()
	go srv.tree.Track(group.Pid(), conn.Done(), trackEvery)
	go srv.watch(group, conn, outR, exited, started)

	ictx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	var init protocol.InitializeResponse
	err = conn.Call(ictx, "initialize", protocol.InitializeParams{
		ClientInfo:   protocol.ClientInfo{Name: "loom", Version: "1"},
		Capabilities: &protocol.InitializeCapabilities{ExperimentalApi: true},
	}, &init)
	if err == nil && s.Root(init.CodexHome) != root {
		err = fmt.Errorf("codex app-server serves %s, not %s: %w", init.CodexHome, root, loomharness.ErrUnavailable)
	}
	if err == nil {
		err = conn.Notify("initialized", nil)
	}
	if err != nil {
		srv.tree.Record(group.Pid())
		group.Kill()
		<-exited
		return fmt.Errorf("codex app-server start for %s: %w", root, err)
	}
	srv.conn, srv.group, srv.exited = conn, group, exited
	return nil
}

// watch waits for the server to exit, kills the rest of its process group
// before the server is reaped (proctree.Group), and reaps its recorded tree;
// its connection ends with a gap for its own threads only, and the next
// Conn starts a new one after the backoff.
func (srv *server) watch(group *proctree.Group, conn *Conn, stdout *os.File, exited chan struct{}, started time.Time) {
	_ = group.Wait()
	_, _ = conn.Close(), stdout.Close()
	<-conn.Done()
	srv.tree.Reap(stopGrace)
	close(exited)
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.group != group {
		return // stopped on purpose, or a start that failed
	}
	srv.conn, srv.group = nil, nil
	if time.Since(started) >= stableAfter {
		srv.failures = 0
	}
	srv.fail()
}

// stop records the server's tree, closes its stdin, which stops an
// app-server, SIGKILLs its process group after stopGrace (only while the
// server is unreaped), and waits until watch has reaped the tree.
func (srv *server) stop() {
	group, exited := srv.group, srv.exited
	if group == nil {
		return
	}
	srv.tree.Record(group.Pid())
	_ = srv.conn.Close()
	srv.conn, srv.group = nil, nil
	select {
	case <-exited:
		return
	case <-time.After(stopGrace):
	}
	group.Kill()
	<-exited
}

// env is the configured environment without GitHub tokens. A root other
// than the inherited one is set as CODEX_HOME; the inherited root keeps the
// user's environment as it is.
func (s *Supervisor) env(root string) []string {
	base := s.cfg.Env
	if base == nil {
		base = os.Environ()
	}
	profile := root != s.Root("")
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if slices.Contains(githubTokens, k) || profile && k == "CODEX_HOME" {
			continue
		}
		out = append(out, kv)
	}
	if profile {
		out = append(out, "CODEX_HOME="+root)
	}
	return out
}

func (s *Supervisor) getenv(key string) string {
	if s.cfg.Env == nil {
		return os.Getenv(key)
	}
	for _, kv := range slices.Backward(s.cfg.Env) {
		if k, v, _ := strings.Cut(kv, "="); k == key {
			return v
		}
	}
	return ""
}

// startPiped starts cmd with stdin and stdout on fresh pipes and returns
// Loom's ends.
func startPiped(cmd *exec.Cmd) (stdin, stdout *os.File, err error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_, _ = inR.Close(), inW.Close()
		return nil, nil, err
	}
	cmd.Stdin, cmd.Stdout = inR, outW
	err = cmd.Start()
	_, _ = inR.Close(), outW.Close()
	if err != nil {
		_, _ = inW.Close(), outR.Close()
		return nil, nil, err
	}
	return inW, outR, nil
}
