package flue

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/netutil"
)

const (
	serverHealthTimeout  = 2 * time.Second
	serverStartupTimeout = 30 * time.Second
)

// Server is a handle to a running flue HTTP server (node dist/server.mjs).
// It is owned by the caller for the lifetime of an interactive session: the
// in-memory agent conversation persists across prompts (and across web-PTY
// reconnects, since the owning loom process stays alive), and Stop tears it
// down when the session ends.
type Server struct {
	url           string
	cmd           *exec.Cmd
	logger        *slog.Logger
	stopOnce      sync.Once
	stopRequested atomic.Bool   // set by Stop so the reaper can tell a crash from a clean stop
	done          chan struct{} // closed by the reaper after cmd exits
}

// URL returns the server's base URL (http://127.0.0.1:<port>).
func (s *Server) URL() string { return s.url }

// StartServer builds the project if needed, then launches a flue HTTP server
// on a free loopback port and returns once GET /healthz reports ready. workDir
// becomes the lead agent's sandbox cwd (via LOOM_WORKTREE_PATH); model is the
// resolved flue model (via LOOM_FLUE_MODEL). The caller owns the server and
// must call Stop.
func (m *Manager) StartServer(ctx context.Context, logger *slog.Logger, workDir, model string) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	_, projectDir, err := m.EnsureProject(ctx)
	if err != nil {
		return nil, err
	}
	dist := filepath.Join(projectDir, serverArtifactRel)
	if !fileExists(dist) {
		return nil, fmt.Errorf("flue backend: built server not found at %s (build may have failed)", dist)
	}
	nodePath, err := resolveNode()
	if err != nil {
		return nil, err
	}
	_, port, err := netutil.PickFreeLoopbackPort()
	if err != nil {
		return nil, fmt.Errorf("flue backend: pick port: %w", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Intentionally NOT detached (no Setpgid): keeping the node server in loom's
	// process group means it dies with loom on any group-delivered signal
	// (terminal Ctrl-C, or SIGHUP when the web "Talk to Lead" PTY closes),
	// avoiding orphans even for signals the lead REPL doesn't explicitly handle.
	// The flue_lead.go handler additionally covers SIGINT/SIGTERM to loom alone.
	// (bootstrap/embedded.go detaches because fleet-db is a shared long-lived
	// service; this server is owned per lead session.)
	cmd := exec.Command(nodePath, dist) //nolint:gosec // nodePath resolved from PATH, dist from managed project
	cmd.Dir = projectDir
	cmd.Env = flueServerEnv(port, workDir, model)
	lw := &lineLogWriter{logger: logger}
	cmd.Stdout = lw
	cmd.Stderr = lw
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("flue backend: start server: %w", err)
	}

	s := &Server{url: url, cmd: cmd, logger: logger, done: make(chan struct{})}
	go func() {
		werr := cmd.Wait()
		if !s.stopRequested.Load() {
			logger.Error("flue lead server exited unexpectedly", "err", werr, "url", url)
		}
		close(s.done)
	}()

	waitCtx, cancel := context.WithTimeout(ctx, serverStartupTimeout)
	defer cancel()
	if err := netutil.WaitForHealthz(waitCtx, url, serverHealthTimeout); err != nil {
		s.Stop()
		return nil, fmt.Errorf("flue backend: server did not become healthy: %w", err)
	}
	logger.Debug("flue lead server ready", "url", url, "pid", cmd.Process.Pid)
	return s, nil
}

// flueServerEnv builds the environment for the flue HTTP server subprocess.
// Provider credentials (ANTHROPIC_API_KEY, etc.) and HOME flow through from
// os.Environ(); app.ts reads ~/.codex/auth.json itself for the codex bridge.
func flueServerEnv(port int, workDir, model string) []string {
	env := append(os.Environ(),
		"PORT="+strconv.Itoa(port),
		"FLUE_MODE=local",
		"LOOM_WORKTREE_PATH="+workDir,
	)
	if model != "" {
		env = append(env, "LOOM_FLUE_MODEL="+model)
	}
	return env
}

// Stop terminates the server (SIGINT, then SIGKILL after a grace period).
// Idempotent.
func (s *Server) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.stopRequested.Store(true)
		if s.cmd == nil || s.cmd.Process == nil {
			return
		}
		_ = s.cmd.Process.Signal(os.Interrupt)
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			_ = s.cmd.Process.Kill()
			<-s.done
		}
	})
}

// lineLogWriter forwards subprocess output to slog at debug level so server
// logs are available (LOOM debug) without polluting the lead REPL's stdout.
type lineLogWriter struct{ logger *slog.Logger }

func (w *lineLogWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			w.logger.Debug("flue-server", "line", line)
		}
	}
	return len(p), nil
}
