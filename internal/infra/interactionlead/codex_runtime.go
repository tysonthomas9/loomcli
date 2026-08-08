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
	"strconv"
	"strings"
	"time"

	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

const (
	defaultCodexBinary           = "codex"
	codexAppServerReadyTimeout   = 60 * time.Second
	codexAppServerLogTailBytes   = 8 * 1024
	codexThreadDiscoveryTimeout  = 45 * time.Second
	codexThreadDiscoveryInterval = 500 * time.Millisecond
)

var codexLeadCurrentExecutable = os.Executable

type CodexLeadRuntimeConfig struct {
	Store     RuntimeStore
	Runtime   SessionRuntime
	Workspace string
	ConfigDir string
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
	runtimeHome, sqliteHome, childEnv, err := prepareCodexLeadRuntime(cfg)
	if err != nil {
		return err
	}

	runtimeStartedAt := time.Now().UTC()
	endpoint, err := freeLoopbackWSEndpoint()
	if err != nil {
		return err
	}
	appServerLogPath := codexAppServerLogPath(runtimeHome)
	appCmd, appErr, cancelApp, logFile, err := startCodexAppServer(ctx, cfg, runtimeHome, sqliteHome, endpoint, childEnv)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	defer cancelApp()

	runtime := persistStartingCodexRuntime(ctx, cfg, endpoint, runtimeHome, sqliteHome, appCmd.Process.Pid)

	if err := waitForCodexAppServer(ctx, endpoint, appErr, appServerLogPath); err != nil {
		_ = stopCodexAppServer(appCmd, appErr, cancelApp)
		runtime.Status = RuntimeStatusFailed
		_ = UpdateCodexRuntimeMetadata(context.Background(), cfg.Runtime, cfg.Workspace, cfg.SessionID, runtime)
		return err
	}

	discoverCtx, cancelDiscover := context.WithCancel(ctx)
	defer cancelDiscover()
	discoverDone := make(chan struct{})
	go discoverCodexLeadThread(discoverCtx, cfg, runtime, runtimeStartedAt, discoverDone)
	drainCtx, cancelDrain := context.WithCancel(ctx)
	defer cancelDrain()
	go drainLeadMessageQueue(drainCtx, cfg.Store, cfg.Runtime, cfg.Workspace, cfg.LeadName, cfg.Logger)

	tuiErr := runCodexRemoteTUI(ctx, cfg, endpoint, childEnv)

	captureCodexTranscriptAfterTUI(
		cfg, runtime, runtimeStartedAt,
		cancelDiscover, discoverDone, cancelDrain,
	)
	if err := stopCodexAppServer(appCmd, appErr, cancelApp); err != nil {
		cfg.Logger.Debug("codex app-server shutdown failed", "err", err)
	}
	runtime.Status = RuntimeStatusDisconnected
	_ = UpdateCodexRuntimeMetadata(context.Background(), cfg.Runtime, cfg.Workspace, cfg.SessionID, runtime)
	return tuiErr
}

func prepareCodexLeadRuntime(cfg CodexLeadRuntimeConfig) (string, string, []string, error) {
	runtimeHome, sqliteHome := codexLeadRuntimeDirs(cfg)
	if err := os.MkdirAll(sqliteHome, 0700); err != nil {
		return "", "", nil, fmt.Errorf("create codex lead runtime directory: %w", err)
	}
	childEnv, err := codexLeadChildEnv(runtimeHome, codexLeadRuntimeBaseEnv(cfg, os.Environ()))
	if err != nil {
		return "", "", nil, err
	}
	return runtimeHome, sqliteHome, childEnv, nil
}

// codexLeadRuntimeBaseEnv filters all ambient LOOM_* values, then adds back
// only the workspace and local data directory selected by the trusted launch
// config. This lets commands run by the interactive Lead resolve the Desktop
// workspace without inheriting stale or forged operator scope or credentials.
func codexLeadRuntimeBaseEnv(cfg CodexLeadRuntimeConfig, base []string) []string {
	env := platformruntime.FilterSubprocessEnv(platformruntime.SubprocessEnvInteractionChild, base)
	workspace := strings.TrimSpace(cfg.Workspace)
	if workspace != "" {
		env = replaceEnvironmentValue(env, "LOOM_WORKSPACE", workspace)
	}
	configDir := strings.TrimSpace(cfg.ConfigDir)
	if configDir != "" {
		env = replaceEnvironmentValue(env, "LOOM_CONFIG_DIR", configDir)
	}
	return env
}

func captureCodexTranscriptAfterTUI(
	cfg CodexLeadRuntimeConfig,
	runtime CodexRuntimeMetadata,
	runtimeStartedAt time.Time,
	cancelDiscover context.CancelFunc,
	discoverDone <-chan struct{},
	cancelDrain context.CancelFunc,
) {
	cancelDiscover()
	<-discoverDone
	cancelDrain()
	captureCtx, cancelCapture := context.WithTimeout(context.Background(), codexTranscriptCaptureTimeout)
	defer cancelCapture()
	if err := captureCodexInteractiveTranscript(captureCtx, cfg, runtime, runtimeStartedAt); err != nil {
		cfg.Logger.Warn("failed to persist codex interactive transcript", "session_id", cfg.SessionID, "err", err)
	}
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
	if err := UpdateCodexRuntimeMetadata(ctx, cfg.Runtime, cfg.Workspace, cfg.SessionID, runtime); err != nil {
		cfg.Logger.Warn("failed to persist codex runtime metadata", "err", err)
	}
	return runtime
}

func startCodexAppServer(
	ctx context.Context,
	cfg CodexLeadRuntimeConfig,
	runtimeHome string,
	sqliteHome string,
	endpoint string,
	childEnv []string,
) (*exec.Cmd, chan error, context.CancelFunc, *os.File, error) {
	// #nosec G304 -- runtimeHome is a lead-scoped cache path derived from Loom workspace/session ids.
	logFile, err := os.OpenFile(codexAppServerLogPath(runtimeHome), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("open codex app-server log: %w", err)
	}
	// The parent context controls the TUI and readiness wait, but the app-server
	// must remain alive long enough to capture the final thread after a normal
	// stop/cancel. Its lifetime is therefore owned by cancelApp and the explicit
	// stopCodexAppServer calls below.
	appCtx, cancelApp := codexAppServerLifetimeContext(ctx)
	// #nosec G204 -- cfg.CodexPath is the configured Codex binary; endpoint/sqliteHome are generated by Loom.
	appCmd := exec.CommandContext(appCtx, cfg.CodexPath, "app-server", "--listen", endpoint, "-c", "sqlite_home="+strconv.Quote(sqliteHome))
	appCmd.Dir = cfg.WorkDir
	appCmd.Env = append([]string(nil), childEnv...)
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

func codexAppServerLifetimeContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(context.WithoutCancel(parent))
}

func codexAppServerLogPath(runtimeHome string) string {
	return filepath.Join(runtimeHome, "app-server.log")
}

func runCodexRemoteTUI(ctx context.Context, cfg CodexLeadRuntimeConfig, endpoint string, childEnv []string) error {
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
	tuiCmd.Env = append([]string(nil), childEnv...)
	tuiCmd.Stdin = cfg.Stdin
	tuiCmd.Stdout = cfg.Stdout
	tuiCmd.Stderr = cfg.Stderr
	return tuiCmd.Run()
}

// codexLeadChildEnv gives each controlled interactive session its own Codex
// state directory. Pointing app-server at an isolated sqlite_home is not
// sufficient: current Codex builds still reconcile the user's global state
// database before opening the listener, which can make startup exceed Loom's
// readiness deadline for long-lived installations. The isolated home keeps
// that reconciliation bounded while symlinks preserve the user's existing
// authentication and configuration without copying credential bytes.
func codexLeadChildEnv(runtimeHome string, baseEnv []string) ([]string, error) {
	runtimeHome = strings.TrimSpace(runtimeHome)
	if runtimeHome == "" {
		return nil, errors.New("codex lead runtime home required")
	}
	sourceHome, err := sourceCodexHome(baseEnv)
	if err != nil {
		return nil, err
	}
	isolatedHome := filepath.Join(runtimeHome, "codex-home")
	if err := os.MkdirAll(isolatedHome, 0700); err != nil {
		return nil, fmt.Errorf("create isolated codex lead home: %w", err)
	}
	// #nosec G302 -- this is a private directory, so owner-only traversal is required.
	if err := os.Chmod(isolatedHome, 0700); err != nil {
		return nil, fmt.Errorf("secure isolated codex lead home: %w", err)
	}
	if err := linkCodexHomeFile(sourceHome, isolatedHome, "auth.json", true); err != nil {
		return nil, err
	}
	if err := linkCodexHomeFile(sourceHome, isolatedHome, "config.toml", false); err != nil {
		return nil, err
	}
	env := replaceEnvironmentValue(baseEnv, "CODEX_HOME", isolatedHome)
	return pinCurrentLoomForCodexShell(runtimeHome, env)
}

// pinCurrentLoomForCodexShell keeps the AI's shell on the same Loom binary
// that launched the controlled session. Codex may use a login shell for tool
// commands, and user startup files can otherwise restore an older global Loom
// after the parent process pins PATH. The private startup directory is loaded
// by zsh, bash, and POSIX shells and the function binding remains authoritative
// even if a later startup file rewrites PATH.
func pinCurrentLoomForCodexShell(runtimeHome string, env []string) ([]string, error) {
	executable, err := codexLeadCurrentExecutable()
	if err != nil {
		return nil, fmt.Errorf("resolve controlled Loom executable: %w", err)
	}
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return nil, errors.New("resolve controlled Loom executable: empty path")
	}
	if !filepath.IsAbs(executable) {
		executable, err = filepath.Abs(executable)
		if err != nil {
			return nil, fmt.Errorf("resolve absolute controlled Loom executable: %w", err)
		}
	}
	executable = filepath.Clean(executable)
	executableDir := filepath.Dir(executable)

	shellHome := filepath.Join(runtimeHome, "shell-home")
	if err := os.MkdirAll(shellHome, 0700); err != nil {
		return nil, fmt.Errorf("create controlled shell home: %w", err)
	}
	// #nosec G302 -- the startup files control executable selection and must be owner-only.
	if err := os.Chmod(shellHome, 0700); err != nil {
		return nil, fmt.Errorf("secure controlled shell home: %w", err)
	}
	startup := "export PATH=" + shellSingleQuote(executableDir) + ":\"${PATH:-}\"\n" +
		"loom() { " + shellSingleQuote(executable) + " \"$@\"; }\n"
	startupPath := filepath.Join(shellHome, "shell-env")
	for _, path := range []string{
		startupPath,
		filepath.Join(shellHome, ".zshenv"),
		filepath.Join(shellHome, ".zprofile"),
	} {
		// #nosec G306 -- these executable-selection files are intentionally owner-only.
		if err := os.WriteFile(path, []byte(startup), 0600); err != nil {
			return nil, fmt.Errorf("write controlled shell startup: %w", err)
		}
		if err := os.Chmod(path, 0600); err != nil {
			return nil, fmt.Errorf("secure controlled shell startup: %w", err)
		}
	}

	env = replaceEnvironmentValue(env, "PATH", prependPathEntry(environmentValue(env, "PATH"), executableDir))
	env = replaceEnvironmentValue(env, "ZDOTDIR", shellHome)
	env = replaceEnvironmentValue(env, "BASH_ENV", startupPath)
	env = replaceEnvironmentValue(env, "ENV", startupPath)
	return env, nil
}

func prependPathEntry(pathValue, entry string) string {
	entries := []string{entry}
	for _, candidate := range filepath.SplitList(pathValue) {
		if filepath.Clean(candidate) != entry {
			entries = append(entries, candidate)
		}
	}
	return strings.Join(entries, string(os.PathListSeparator))
}

func environmentValue(env []string, name string) string {
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key == name {
			return value
		}
	}
	return ""
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func sourceCodexHome(baseEnv []string) (string, error) {
	for _, entry := range baseEnv {
		name, value, ok := strings.Cut(entry, "=")
		if ok && name == "CODEX_HOME" && strings.TrimSpace(value) != "" {
			if !filepath.IsAbs(value) {
				return "", errors.New("CODEX_HOME must be an absolute path")
			}
			return filepath.Clean(value), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", errors.New("resolve Codex authentication home")
	}
	return filepath.Join(home, ".codex"), nil
}

func linkCodexHomeFile(sourceHome, isolatedHome, name string, required bool) error {
	source := filepath.Join(sourceHome, name)
	info, err := os.Stat(source)
	if err != nil {
		if !required && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("find Codex %s in configured home: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("codex %s in configured home is not a regular file", name)
	}
	target := filepath.Join(isolatedHome, name)
	if existing, err := os.Lstat(target); err == nil {
		if existing.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("isolated Codex %s already exists and is not a symlink", name)
		}
		linked, err := os.Readlink(target)
		if err != nil {
			return fmt.Errorf("read isolated Codex %s link: %w", name, err)
		}
		if filepath.Clean(linked) != filepath.Clean(source) {
			return fmt.Errorf("isolated Codex %s points outside the configured home", name)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect isolated Codex %s: %w", name, err)
	}
	if err := os.Symlink(source, target); err != nil {
		return fmt.Errorf("link Codex %s into isolated lead home: %w", name, err)
	}
	return nil
}

func replaceEnvironmentValue(base []string, name, value string) []string {
	out := make([]string, 0, len(base)+1)
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok && key == name {
			continue
		}
		out = append(out, entry)
	}
	return append(out, name+"="+value)
}

func normalizeCodexLeadRuntimeConfig(cfg CodexLeadRuntimeConfig) CodexLeadRuntimeConfig {
	cfg.Workspace = strings.TrimSpace(cfg.Workspace)
	cfg.ConfigDir = strings.TrimSpace(cfg.ConfigDir)
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
	return runtimeHome, filepath.Join(runtimeHome, "sqlite")
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

func discoverCodexLeadThread(
	ctx context.Context,
	cfg CodexLeadRuntimeConfig,
	runtime CodexRuntimeMetadata,
	runtimeStartedAt time.Time,
	done chan<- struct{},
) {
	defer close(done)
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
			_ = MarkAssignmentDeliveryAttempt(ctx, cfg.Runtime, cfg.Workspace, cfg.SessionID, "codex thread discovery timed out")
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
			if err := UpdateCodexRuntimeMetadata(ctx, cfg.Runtime, cfg.Workspace, cfg.SessionID, runtime); err != nil {
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
