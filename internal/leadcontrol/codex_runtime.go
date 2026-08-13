package leadcontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	defaultCodexBinary           = "codex"
	codexAppServerReadyTimeout   = 60 * time.Second
	codexAppServerLogTailBytes   = 8 * 1024
	codexThreadDiscoveryTimeout  = 45 * time.Second
	codexThreadDiscoveryInterval = 500 * time.Millisecond
)

type CodexLeadRuntimeConfig struct {
	Store     store.Store
	Workspace string
	LeadName  string
	SessionID string
	WorkDir   string
	Prompt    string
	CodexPath string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	Logger    *slog.Logger
}

func RunCodexLeadRuntime(ctx context.Context, cfg CodexLeadRuntimeConfig) error {
	cfg = normalizeCodexLeadRuntimeConfig(cfg)
	runtimeHome, sqliteHome := codexLeadRuntimeDirs(cfg)
	// runtimeHome holds loom's own per-session artifacts (app-server.log). sqliteHome
	// is now codex's OWN home, which codex manages — loom must not create or seed it.
	if err := os.MkdirAll(runtimeHome, 0700); err != nil {
		return fmt.Errorf("create codex lead runtime directory: %w", err)
	}

	runtimeStartedAt := time.Now().UTC()
	endpoint, err := freeLoopbackWSEndpoint()
	if err != nil {
		return err
	}
	appServerLogPath := codexAppServerLogPath(runtimeHome)
	appCmd, appErr, cancelApp, logFile, err := startCodexAppServer(ctx, cfg, runtimeHome, endpoint)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	defer cancelApp()

	runtime := persistStartingCodexRuntime(ctx, cfg, endpoint, runtimeHome, sqliteHome, appCmd.Process.Pid)

	if err := waitForCodexAppServer(ctx, endpoint, appErr, appServerLogPath); err != nil {
		_ = stopCodexAppServer(appCmd, appErr, cancelApp)
		runtime.Status = RuntimeStatusFailed
		_ = UpdateCodexRuntimeMetadata(context.Background(), cfg.Store, cfg.Workspace, cfg.SessionID, runtime)
		return err
	}

	discoverCtx, cancelDiscover := context.WithCancel(ctx)
	defer cancelDiscover()
	go discoverCodexLeadThread(discoverCtx, cfg, runtime, runtimeStartedAt)
	drainCtx, cancelDrain := context.WithCancel(ctx)
	defer cancelDrain()
	go drainLeadMessageQueue(drainCtx, cfg.Store, cfg.Workspace, cfg.LeadName, cfg.Logger)

	tuiErr := runCodexRemoteTUI(ctx, cfg, endpoint)

	cancelDiscover()
	cancelDrain()
	if err := stopCodexAppServer(appCmd, appErr, cancelApp); err != nil {
		cfg.Logger.Debug("codex app-server shutdown failed", "err", err)
	}
	runtime.Status = RuntimeStatusDisconnected
	_ = UpdateCodexRuntimeMetadata(context.Background(), cfg.Store, cfg.Workspace, cfg.SessionID, runtime)
	return tuiErr
}

// persistStartingCodexRuntime builds the starting runtime metadata for a
// freshly launched app server and persists it onto the lead session.
func persistStartingCodexRuntime(ctx context.Context, cfg CodexLeadRuntimeConfig, endpoint, runtimeHome, sqliteHome string, pid int) CodexRuntimeMetadata {
	runtime := CodexRuntimeMetadata{
		Endpoint:    endpoint,
		RuntimeHome: runtimeHome,
		SQLiteHome:  sqliteHome,
		PID:         pid,
		Status:      RuntimeStatusStarting,
		Controlled:  true,
	}
	if err := UpdateCodexRuntimeMetadata(ctx, cfg.Store, cfg.Workspace, cfg.SessionID, runtime); err != nil {
		cfg.Logger.Warn("failed to persist codex runtime metadata", "err", err)
	}
	return runtime
}

// codexAppServerArgs builds the app-server argv.
//
// Deliberately NO `-c sqlite_home=...`. Pointing codex at a sqlite home it has not
// already backfilled makes `app-server` block on the state-db backfill in the DEFAULT
// home and never bind its listener — silently, because the explanation is printed only
// under --strict-config, so loom's app-server.log tail is empty and the failure looks
// like a mystery 60s timeout. Measured on codex 0.145.0: with the flag, no listener
// within 100s; without it, listening in under a second. Reusing one stable directory
// does not help — the first launch writes goals/logs/memories but never
// state_5.sqlite, so the home never becomes usable. Codex therefore uses its own home
// (CODEX_HOME, else ~/.codex), the only home that is reliably backfilled.
//
// Extracted from startCodexAppServer purely so this invariant is testable without
// spawning a process.
func codexAppServerArgs(endpoint string) []string {
	return []string{"app-server", "--listen", endpoint}
}

// sqliteHome is deliberately NOT a parameter: the `-c sqlite_home=…` override was
// removed (it wedges codex app-server startup on 0.145.0), so this function has no
// use for it. The value is still recorded as runtime metadata by the caller.
func startCodexAppServer(
	ctx context.Context,
	cfg CodexLeadRuntimeConfig,
	runtimeHome string,
	endpoint string,
) (*exec.Cmd, chan error, context.CancelFunc, *os.File, error) {
	// #nosec G304 -- runtimeHome is a lead-scoped cache path derived from Loom workspace/session ids.
	logFile, err := os.OpenFile(codexAppServerLogPath(runtimeHome), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("open codex app-server log: %w", err)
	}
	appCtx, cancelApp := context.WithCancel(ctx)
	// #nosec G204 -- cfg.CodexPath is the configured Codex binary; endpoint is generated by Loom.
	appCmd := exec.CommandContext(appCtx, cfg.CodexPath, codexAppServerArgs(endpoint)...)
	appCmd.Dir = cfg.WorkDir
	appCmd.Env = os.Environ()
	appCmd.Stdout = logFile
	appCmd.Stderr = logFile
	if err := appCmd.Start(); err != nil {
		cancelApp()
		_ = logFile.Close()
		return nil, nil, nil, nil, fmt.Errorf("start codex app-server: %w", err)
	}
	appErr := make(chan error, 1)
	go func() {
		appErr <- appCmd.Wait()
		close(appErr)
	}()
	return appCmd, appErr, cancelApp, logFile, nil
}

func codexAppServerLogPath(runtimeHome string) string {
	return filepath.Join(runtimeHome, "app-server.log")
}

func runCodexRemoteTUI(ctx context.Context, cfg CodexLeadRuntimeConfig, endpoint string) error {
	_, _ = fmt.Fprintln(cfg.Stdout, "Launching controlled Codex lead session...")
	_, _ = fmt.Fprintln(cfg.Stdout, "")
	// #nosec G204 -- cfg.CodexPath/workDir/prompt are the same trusted inputs used by interactive agent launch.
	tuiCmd := exec.CommandContext(ctx, cfg.CodexPath,
		"--remote", endpoint,
		"--no-alt-screen",
		"--dangerously-bypass-approvals-and-sandbox",
		"-C", cfg.WorkDir,
		cfg.Prompt,
	)
	tuiCmd.Dir = cfg.WorkDir
	tuiCmd.Env = os.Environ()
	tuiCmd.Stdin = cfg.Stdin
	tuiCmd.Stdout = cfg.Stdout
	tuiCmd.Stderr = cfg.Stderr
	return tuiCmd.Run()
}

func normalizeCodexLeadRuntimeConfig(cfg CodexLeadRuntimeConfig) CodexLeadRuntimeConfig {
	cfg.Workspace = strings.TrimSpace(cfg.Workspace)
	cfg.LeadName = strings.TrimSpace(cfg.LeadName)
	cfg.SessionID = strings.TrimSpace(cfg.SessionID)
	cfg.WorkDir = strings.TrimSpace(cfg.WorkDir)
	if cfg.CodexPath == "" {
		cfg.CodexPath = defaultCodexBinary
	}
	if cfg.Stdin == nil {
		cfg.Stdin = os.Stdin
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}

func codexLeadRuntimeDirs(cfg CodexLeadRuntimeConfig) (string, string) {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	workspace := sanitizeRuntimePathPart(cfg.Workspace)
	if workspace == "" {
		workspace = "workspace"
	}
	lead := sanitizeRuntimePathPart(cfg.LeadName)
	if lead == "" {
		lead = "lead"
	}
	session := sanitizeRuntimePathPart(cfg.SessionID)
	if session == "" {
		session = "session"
	}
	runtimeHome := filepath.Join(base, "loom", "codex-leads", workspace, lead, session)
	return runtimeHome, effectiveCodexHome()
}

// effectiveCodexHome reports the sqlite/state home codex will actually use, for
// session metadata. Loom no longer overrides it (see startCodexAppServer), so this
// mirrors codex's own resolution rather than inventing a path loom controls.
func effectiveCodexHome() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return home
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".codex")
	}
	return ""
}

func sanitizeRuntimePathPart(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + ('a' - 'A'))
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func freeLoopbackWSEndpoint() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate codex app-server port: %w", err)
	}
	defer func() { _ = ln.Close() }()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return "", fmt.Errorf("allocate codex app-server port: unexpected addr %s", ln.Addr())
	}
	return fmt.Sprintf("ws://127.0.0.1:%d", addr.Port), nil
}

func waitForCodexAppServer(ctx context.Context, endpoint string, appErr <-chan error, logPath string) error {
	deadline := time.NewTimer(codexAppServerReadyTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastProbeErr error
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-appErr:
			if err == nil {
				err = errors.New("codex app-server exited")
			}
			return fmt.Errorf("codex app-server exited before ready: %w", err)
		case <-deadline.C:
			return codexAppServerTimeoutError(endpoint, codexAppServerReadyTimeout, lastProbeErr, logPath)
		case <-ticker.C:
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			client, err := dialCodexAppServerClient(probeCtx, endpoint)
			cancel()
			if err == nil {
				_ = client.Close("ready probe complete")
				return nil
			}
			lastProbeErr = err
		}
	}
}

func codexAppServerTimeoutError(endpoint string, timeout time.Duration, lastProbeErr error, logPath string) error {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "codex app-server did not become ready at %s within %s", endpoint, timeout)
	if lastProbeErr != nil {
		_, _ = fmt.Fprintf(&b, " (last readiness probe: %v)", lastProbeErr)
	}
	if tail := strings.TrimSpace(readFileTail(logPath, codexAppServerLogTailBytes)); tail != "" {
		_, _ = fmt.Fprintf(&b, "\napp-server log tail:\n%s", tail)
	}
	return errors.New(b.String())
}

func readFileTail(path string, limit int64) string {
	if strings.TrimSpace(path) == "" || limit <= 0 {
		return ""
	}
	// #nosec G304 -- path is the lead-scoped app-server log path generated by Loom.
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return ""
	}
	offset := info.Size() - limit
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	return string(data)
}

func discoverCodexLeadThread(ctx context.Context, cfg CodexLeadRuntimeConfig, runtime CodexRuntimeMetadata, runtimeStartedAt time.Time) {
	deadline := time.NewTimer(codexThreadDiscoveryTimeout)
	defer deadline.Stop()
	// Each probe dials the app server, so back off exponentially on misses
	// instead of paying a fixed-rate connection cost for slow startups.
	interval := codexThreadDiscoveryInterval
	probe := time.NewTimer(interval)
	defer probe.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			_ = MarkAssignmentDeliveryAttempt(ctx, cfg.Store, cfg.Workspace, cfg.SessionID, "codex thread discovery timed out")
			return
		case <-probe.C:
			thread, err := findNewestCodexThread(ctx, runtime.Endpoint, cfg.WorkDir, runtimeStartedAt)
			if err != nil || thread == nil {
				interval *= 2
				if interval > 5*time.Second {
					interval = 5 * time.Second
				}
				probe.Reset(interval)
				continue
			}
			runtime.ThreadID = thread.ID
			runtime.Status = thread.Status.RuntimeStatus()
			if err := UpdateCodexRuntimeMetadata(ctx, cfg.Store, cfg.Workspace, cfg.SessionID, runtime); err != nil {
				cfg.Logger.Debug("failed to persist codex thread metadata", "err", err)
			}
			return
		}
	}
}

func findNewestCodexThread(ctx context.Context, endpoint, workDir string, createdAfter time.Time) (*CodexThread, error) {
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	client, err := dialCodexAppServerClient(callCtx, endpoint)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close("thread discovery complete") }()
	threads, err := client.ListThreads(callCtx, workDir, 8)
	if err != nil {
		return nil, err
	}
	return newestCodexThread(threads, workDir, createdAfter), nil
}

func newestCodexThread(threads []CodexThread, workDir string, createdAfter time.Time) *CodexThread {
	workDir = strings.TrimSpace(workDir)
	if !createdAfter.IsZero() {
		createdAfter = createdAfter.Add(-2 * time.Second)
	}
	var best *CodexThread
	for i := range threads {
		thread := threads[i]
		if strings.TrimSpace(thread.ID) == "" {
			continue
		}
		if workDir != "" && strings.TrimSpace(thread.Cwd) != workDir {
			continue
		}
		if !createdAfter.IsZero() {
			createdAt := threadCreatedAt(thread)
			if createdAt.IsZero() || createdAt.Before(createdAfter) {
				continue
			}
		}
		if best == nil || threadSortTime(thread).After(threadSortTime(*best)) {
			best = &thread
		}
	}
	return best
}

func threadCreatedAt(thread CodexThread) time.Time {
	if t := unixFloatTime(thread.CreatedAtMS); !t.IsZero() {
		return t
	}
	return unixFloatTime(thread.CreatedAt)
}

func threadSortTime(thread CodexThread) time.Time {
	for _, value := range []float64{thread.UpdatedAtMS, thread.UpdatedAt, thread.CreatedAtMS, thread.CreatedAt} {
		if t := unixFloatTime(value); !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

func unixFloatTime(value float64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	if value > 1e12 {
		return time.UnixMilli(int64(value)).UTC()
	}
	seconds := int64(value)
	nanos := int64((value - float64(seconds)) * 1e9)
	return time.Unix(seconds, nanos).UTC()
}

func stopCodexAppServer(cmd *exec.Cmd, appErr <-chan error, cancel context.CancelFunc) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	cancel()
	select {
	case err := <-appErr:
		return err
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		select {
		case err := <-appErr:
			return err
		case <-time.After(2 * time.Second):
			return errors.New("codex app-server did not exit after kill")
		}
	}
}
