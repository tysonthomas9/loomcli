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
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	defaultCodexBinary           = "codex"
	codexAppServerReadyTimeout   = 60 * time.Second
	codexAppServerLogTailBytes   = 8 * 1024
	codexThreadDiscoveryTimeout  = 45 * time.Second
	codexThreadDiscoveryInterval = 500 * time.Millisecond
	codexResumeReadAttempts      = 3
	codexResumeReadTimeout       = 1200 * time.Millisecond
	codexResumeRetryInterval     = 150 * time.Millisecond
	codexResumeSettlementTimeout = 8 * time.Second
)

const codexResumeReanchorPrompt = "Resume your existing Loom lead thread after the pause. You are still the lead agent; continue from the existing context."

type CodexLeadRuntimeConfig struct {
	Store          store.Store
	Workspace      string
	LeadName       string
	SessionID      string
	WorkDir        string
	Prompt         string
	CodexPath      string
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
	Logger         *slog.Logger
	ResumeEligible bool
}

func RunCodexLeadRuntime(ctx context.Context, cfg CodexLeadRuntimeConfig) error {
	cfg = normalizeCodexLeadRuntimeConfig(cfg)
	priorRuntime, err := snapshotCodexRuntime(ctx, cfg)
	if err != nil {
		cfg.Logger.Warn("failed to read prior codex runtime metadata; launching fresh", "err", err)
		priorRuntime = CodexRuntimeMetadata{}
	}
	if priorCodexRuntimeLooksLive(ctx, priorRuntime) {
		return fmt.Errorf(
			"controlled Codex runtime already appears live for session %s (pid %d, endpoint %s, status %s); refusing duplicate launch",
			cfg.SessionID,
			priorRuntime.PID,
			priorRuntime.Endpoint,
			priorRuntime.Status,
		)
	}

	runtimeHome, sqliteHome := codexLeadRuntimeDirs(cfg)
	if err := os.MkdirAll(sqliteHome, 0700); err != nil {
		return fmt.Errorf("create codex lead runtime directory: %w", err)
	}

	runtimeStartedAt := time.Now().UTC()
	endpoint, err := freeLoopbackWSEndpoint()
	if err != nil {
		return err
	}
	appServerLogPath := codexAppServerLogPath(runtimeHome)
	appCmd, appErr, cancelApp, logFile, err := startCodexAppServer(ctx, cfg, runtimeHome, sqliteHome, endpoint)
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

	resumeDecision := decideCodexResume(ctx, cfg, priorRuntime, func(readCtx context.Context, threadID string) (*CodexThread, error) {
		return readCodexThreadForResume(readCtx, endpoint, threadID)
	})
	if resumeDecision.Attempted && !resumeDecision.Resume {
		cfg.Logger.Warn("codex thread resume unavailable; launching fresh", "thread", priorRuntime.ThreadID, "reason", resumeDecision.Reason)
	}
	if resumeDecision.ClearThreadID {
		clearCodexThreadMetadata(ctx, cfg, runtime)
	}

	tuiErr := runCodexTUIWithOptionalResume(ctx, cfg, endpoint, runtime, priorRuntime.ThreadID, resumeDecision, runtimeStartedAt)

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

// runCodexTUIWithOptionalResume runs the fresh-boot TUI, or — when the resume
// decision took — the resume TUI with post-launch reconciliation and gated
// draining, relaunching fresh in-process exactly once if resume exits before it
// is ready. It always returns the last TUI's error (nil on clean exit).
func runCodexTUIWithOptionalResume(
	ctx context.Context,
	cfg CodexLeadRuntimeConfig,
	endpoint string,
	runtime CodexRuntimeMetadata,
	priorThreadID string,
	resumeDecision codexResumeDecision,
	runtimeStartedAt time.Time,
) error {
	if !resumeDecision.Resume {
		discoverCtx, cancelDiscover := context.WithCancel(ctx)
		go discoverCodexLeadThread(discoverCtx, cfg, runtime, runtimeStartedAt)
		drainCtx, cancelDrain := context.WithCancel(ctx)
		go drainLeadMessageQueue(drainCtx, cfg.Store, cfg.Workspace, cfg.LeadName, cfg.Logger)
		tuiErr := runCodexRemoteTUI(ctx, cfg, endpoint)
		cancelDiscover()
		cancelDrain()
		return tuiErr
	}

	resumeStartedAt := time.Now().UTC()
	reconcileCtx, cancelReconcile := context.WithCancel(ctx)
	outcomes := make(chan codexResumeReconciliation, 1)
	reconcileDone := make(chan struct{})
	go func() {
		defer close(reconcileDone)
		reconcileResumedCodexThread(reconcileCtx, cfg, runtime, *resumeDecision.Thread, resumeStartedAt, outcomes)
	}()

	drainCtx, cancelDrain := context.WithCancel(ctx)
	resumeReady := make(chan struct{})
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		drainAfterCodexResumeReconciliation(drainCtx, cfg, outcomes, resumeReady)
	}()

	tuiErr := runCodexResumeTUI(ctx, cfg, endpoint, priorThreadID)
	relaunchFresh := tuiErr != nil && !channelClosed(resumeReady)
	cancelReconcile()
	cancelDrain()
	<-reconcileDone
	<-drainDone
	if !relaunchFresh {
		return tuiErr
	}
	cfg.Logger.Warn("codex resume exited before thread reconciliation; relaunching fresh", "thread", priorThreadID, "err", tuiErr)
	return relaunchFreshCodexTUI(ctx, cfg, endpoint, runtime)
}

// relaunchFreshCodexTUI clears the stale thread id and runs a fresh-boot TUI
// against the still-running app server, with its own discovery timestamp.
func relaunchFreshCodexTUI(ctx context.Context, cfg CodexLeadRuntimeConfig, endpoint string, runtime CodexRuntimeMetadata) error {
	clearCodexThreadMetadata(context.Background(), cfg, runtime)
	freshStartedAt := time.Now().UTC()
	discoverCtx, cancelDiscover := context.WithCancel(ctx)
	go discoverCodexLeadThread(discoverCtx, cfg, runtime, freshStartedAt)
	freshDrainCtx, cancelFreshDrain := context.WithCancel(ctx)
	go drainLeadMessageQueue(freshDrainCtx, cfg.Store, cfg.Workspace, cfg.LeadName, cfg.Logger)
	tuiErr := runCodexRemoteTUI(ctx, cfg, endpoint)
	cancelDiscover()
	cancelFreshDrain()
	return tuiErr
}

func snapshotCodexRuntime(ctx context.Context, cfg CodexLeadRuntimeConfig) (CodexRuntimeMetadata, error) {
	if cfg.Store == nil || cfg.Store.AgentSessions() == nil || cfg.Workspace == "" || cfg.SessionID == "" {
		return CodexRuntimeMetadata{}, nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	session, err := cfg.Store.AgentSessions().Get(readCtx, cfg.Workspace, cfg.SessionID)
	if errors.Is(err, domain.ErrNotFound) {
		return CodexRuntimeMetadata{}, nil
	}
	if err != nil {
		return CodexRuntimeMetadata{}, err
	}
	return RuntimeMetadataFromSession(session), nil
}

func priorCodexRuntimeLooksLive(ctx context.Context, runtime CodexRuntimeMetadata) bool {
	if !duplicateCodexRuntimeLive(runtime, codexProcessAlive(runtime.PID)) {
		return false
	}
	// The recorded process is alive with a live-ish status; only refuse the
	// launch if its app server still answers, so a stale record left by a
	// crashed runtime cannot block a legitimate revive.
	probeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	client, err := dialCodexAppServerClient(probeCtx, runtime.Endpoint)
	if err != nil {
		return false
	}
	_ = client.Close("duplicate runtime probe complete")
	return true
}

func duplicateCodexRuntimeLive(runtime CodexRuntimeMetadata, processAlive bool) bool {
	if !runtime.Controlled || runtime.Endpoint == "" || runtime.PID <= 0 || !processAlive {
		return false
	}
	switch runtime.Status {
	case RuntimeStatusIdle, RuntimeStatusActive, RuntimeStatusWaitingApproval, RuntimeStatusWaitingUserInput, "running", "ready":
		return true
	default:
		return false
	}
}

func codexProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

type codexResumeDecision struct {
	Resume        bool
	Attempted     bool
	ClearThreadID bool
	Thread        *CodexThread
	Reason        string
}

func decideCodexResume(
	ctx context.Context,
	cfg CodexLeadRuntimeConfig,
	prior CodexRuntimeMetadata,
	readThread func(context.Context, string) (*CodexThread, error),
) codexResumeDecision {
	if !cfg.ResumeEligible {
		return codexResumeDecision{Reason: "runtime is not resume-eligible"}
	}
	if cfg.Store == nil || cfg.SessionID == "" {
		return codexResumeDecision{Reason: "runtime session metadata is unavailable"}
	}
	threadID := strings.TrimSpace(prior.ThreadID)
	if threadID == "" {
		return codexResumeDecision{Reason: "no prior codex thread id"}
	}
	decision := codexResumeDecision{Attempted: true, ClearThreadID: true}
	thread, err := readThread(ctx, threadID)
	if err != nil {
		decision.Reason = err.Error()
		return decision
	}
	if thread == nil {
		decision.Reason = "codex thread/read returned no thread"
		return decision
	}
	if strings.TrimSpace(thread.ID) != threadID {
		decision.Reason = fmt.Sprintf("codex thread/read returned id %q", strings.TrimSpace(thread.ID))
		return decision
	}
	if strings.TrimSpace(thread.Cwd) != normalizeCodexWorkDir(cfg.WorkDir) {
		decision.Reason = fmt.Sprintf("codex thread cwd %q does not match %q", strings.TrimSpace(thread.Cwd), normalizeCodexWorkDir(cfg.WorkDir))
		return decision
	}
	decision.Resume = true
	decision.ClearThreadID = false
	decision.Thread = thread
	return decision
}

func readCodexThreadForResume(ctx context.Context, endpoint, threadID string) (*CodexThread, error) {
	var lastErr error
	for attempt := 0; attempt < codexResumeReadAttempts; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, codexResumeReadTimeout)
		client, err := dialCodexAppServerClient(callCtx, endpoint)
		if err == nil {
			var thread *CodexThread
			thread, err = client.ReadThread(callCtx, threadID)
			_ = client.Close("resume validation complete")
			if err == nil {
				cancel()
				return thread, nil
			}
		}
		cancel()
		lastErr = err
		if attempt+1 == codexResumeReadAttempts {
			break
		}
		timer := time.NewTimer(codexResumeRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr == nil {
		lastErr = errors.New("codex thread resume validation failed")
	}
	return nil, lastErr
}

func normalizeCodexWorkDir(workDir string) string {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return ""
	}
	return filepath.Clean(workDir)
}

func clearCodexThreadMetadata(ctx context.Context, cfg CodexLeadRuntimeConfig, runtime CodexRuntimeMetadata) {
	clearCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	clearRuntime := runtime
	clearRuntime.ClearThreadID = true
	if err := UpdateCodexRuntimeMetadata(clearCtx, cfg.Store, cfg.Workspace, cfg.SessionID, clearRuntime); err != nil {
		cfg.Logger.Warn("failed to clear stale codex thread metadata", "err", err)
	}
}

type codexResumeReconciliation struct {
	ThreadID string
	Resumed  bool
	Reason   string
}

func reconcileResumedCodexThread(
	ctx context.Context,
	cfg CodexLeadRuntimeConfig,
	runtime CodexRuntimeMetadata,
	prior CodexThread,
	launchedAt time.Time,
	outcomes chan<- codexResumeReconciliation,
) {
	defer close(outcomes)
	deadline := time.NewTimer(codexResumeSettlementTimeout)
	defer deadline.Stop()
	probe := time.NewTicker(codexThreadDiscoveryInterval)
	defer probe.Stop()
	latestKnown := prior
	for {
		select {
		case <-ctx.Done():
			outcomes <- codexResumeReconciliation{Reason: ctx.Err().Error()}
			return
		case <-deadline.C:
			cfg.Logger.Warn("codex resume settlement elapsed without post-launch thread evidence", "thread", prior.ID)
			persistReconciledCodexThread(ctx, cfg, runtime, latestKnown)
			outcomes <- codexResumeReconciliation{
				ThreadID: prior.ID,
				Resumed:  true,
				Reason:   "settlement window elapsed without contradictory thread evidence",
			}
			return
		case <-probe.C:
			outcome, updated := evaluateCodexResumeProbe(ctx, cfg, runtime, prior, latestKnown, launchedAt)
			latestKnown = updated
			if outcome != nil {
				outcomes <- *outcome
				return
			}
		}
	}
}

func evaluateCodexResumeProbe(
	ctx context.Context,
	cfg CodexLeadRuntimeConfig,
	runtime CodexRuntimeMetadata,
	prior CodexThread,
	latestKnown CodexThread,
	launchedAt time.Time,
) (*codexResumeReconciliation, CodexThread) {
	threads, err := listCodexThreadsForResume(ctx, runtime.Endpoint, cfg.WorkDir)
	if err != nil {
		return nil, latestKnown
	}
	if replacement := newestCodexResumeReplacement(threads, prior.ID, cfg.WorkDir, launchedAt); replacement != nil {
		cfg.Logger.Error("codex resume did not take; adopting replacement thread", "requested_thread", prior.ID, "authoritative_thread", replacement.ID)
		persistReconciledCodexThread(ctx, cfg, runtime, *replacement)
		return &codexResumeReconciliation{
			ThreadID: replacement.ID,
			Reason:   "resume launched a replacement thread",
		}, latestKnown
	}
	known := codexThreadByID(threads, prior.ID, cfg.WorkDir)
	if known == nil {
		return nil, latestKnown
	}
	latestKnown = *known
	if !codexResumedThreadHasPostLaunchEvidence(prior, *known, launchedAt) {
		return nil, latestKnown
	}
	persistReconciledCodexThread(ctx, cfg, runtime, *known)
	return &codexResumeReconciliation{
		ThreadID: known.ID,
		Resumed:  true,
		Reason:   "requested thread produced post-launch evidence",
	}, latestKnown
}

func listCodexThreadsForResume(ctx context.Context, endpoint, workDir string) ([]CodexThread, error) {
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	client, err := dialCodexAppServerClient(callCtx, endpoint)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close("resume reconciliation complete") }()
	return client.ListThreads(callCtx, normalizeCodexWorkDir(workDir), 8)
}

func newestCodexResumeReplacement(threads []CodexThread, priorID, workDir string, launchedAt time.Time) *CodexThread {
	priorID = strings.TrimSpace(priorID)
	workDir = normalizeCodexWorkDir(workDir)
	postLaunchThreshold := launchedAt.Add(-2 * time.Second)
	var best *CodexThread
	for i := range threads {
		thread := threads[i]
		if strings.TrimSpace(thread.ID) == "" || strings.TrimSpace(thread.ID) == priorID {
			continue
		}
		if workDir != "" && strings.TrimSpace(thread.Cwd) != workDir {
			continue
		}
		evidenceAt := threadCreatedAt(thread)
		if evidenceAt.IsZero() {
			evidenceAt = threadSortTime(thread)
		}
		if evidenceAt.IsZero() || evidenceAt.Before(postLaunchThreshold) {
			continue
		}
		if best == nil || threadSortTime(thread).After(threadSortTime(*best)) {
			best = &thread
		}
	}
	return best
}

func codexThreadByID(threads []CodexThread, threadID, workDir string) *CodexThread {
	threadID = strings.TrimSpace(threadID)
	workDir = normalizeCodexWorkDir(workDir)
	for i := range threads {
		if strings.TrimSpace(threads[i].ID) != threadID {
			continue
		}
		if workDir != "" && strings.TrimSpace(threads[i].Cwd) != workDir {
			continue
		}
		return &threads[i]
	}
	return nil
}

func codexResumedThreadHasPostLaunchEvidence(prior, current CodexThread, launchedAt time.Time) bool {
	if prior.Status.RuntimeStatus() != current.Status.RuntimeStatus() {
		return true
	}
	priorTime := threadSortTime(prior)
	currentTime := threadSortTime(current)
	return currentTime.After(priorTime) && currentTime.After(launchedAt.Add(-2*time.Second))
}

func persistReconciledCodexThread(ctx context.Context, cfg CodexLeadRuntimeConfig, runtime CodexRuntimeMetadata, thread CodexThread) {
	runtime.ThreadID = strings.TrimSpace(thread.ID)
	runtime.Status = thread.Status.RuntimeStatus()
	if err := UpdateCodexRuntimeMetadata(ctx, cfg.Store, cfg.Workspace, cfg.SessionID, runtime); err != nil {
		cfg.Logger.Warn("failed to persist reconciled codex thread metadata", "err", err)
	}
}

func drainAfterCodexResumeReconciliation(
	ctx context.Context,
	cfg CodexLeadRuntimeConfig,
	outcomes <-chan codexResumeReconciliation,
	ready chan<- struct{},
) {
	select {
	case <-ctx.Done():
		return
	case outcome, ok := <-outcomes:
		if !ok || strings.TrimSpace(outcome.ThreadID) == "" {
			return
		}
		close(ready)
		drainLeadMessageQueue(ctx, cfg.Store, cfg.Workspace, cfg.LeadName, cfg.Logger)
	}
}

func channelClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func startCodexAppServer(
	ctx context.Context,
	cfg CodexLeadRuntimeConfig,
	runtimeHome string,
	sqliteHome string,
	endpoint string,
) (*exec.Cmd, chan error, context.CancelFunc, *os.File, error) {
	// #nosec G304 -- runtimeHome is a lead-scoped cache path derived from Loom workspace/session ids.
	logFile, err := os.OpenFile(codexAppServerLogPath(runtimeHome), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("open codex app-server log: %w", err)
	}
	appCtx, cancelApp := context.WithCancel(ctx)
	// #nosec G204 -- cfg.CodexPath is the configured Codex binary; endpoint/sqliteHome are generated by Loom.
	appCmd := exec.CommandContext(appCtx, cfg.CodexPath, "app-server", "--listen", endpoint, "-c", "sqlite_home="+strconv.Quote(sqliteHome))
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
	return runCodexTUI(ctx, cfg, freshCodexTUIArgs(cfg, endpoint))
}

func freshCodexTUIArgs(cfg CodexLeadRuntimeConfig, endpoint string) []string {
	return []string{
		"--remote", endpoint,
		"--no-alt-screen",
		"--dangerously-bypass-approvals-and-sandbox",
		"-C", cfg.WorkDir,
		cfg.Prompt,
	}
}

func runCodexResumeTUI(ctx context.Context, cfg CodexLeadRuntimeConfig, endpoint, threadID string) error {
	_, _ = fmt.Fprintln(cfg.Stdout, "Resuming controlled Codex lead session...")
	_, _ = fmt.Fprintln(cfg.Stdout, "")
	return runCodexTUI(ctx, cfg, resumeCodexTUIArgs(cfg, endpoint, threadID))
}

func resumeCodexTUIArgs(cfg CodexLeadRuntimeConfig, endpoint, threadID string) []string {
	return []string{
		"resume", threadID,
		"--remote", endpoint,
		"--no-alt-screen",
		"--dangerously-bypass-approvals-and-sandbox",
		"-C", cfg.WorkDir,
		codexResumeReanchorPrompt,
	}
}

func runCodexTUI(ctx context.Context, cfg CodexLeadRuntimeConfig, args []string) error {
	// #nosec G204 -- cfg.CodexPath/workDir/prompt are the same trusted inputs used by interactive agent launch.
	tuiCmd := exec.CommandContext(ctx, cfg.CodexPath, args...)
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
