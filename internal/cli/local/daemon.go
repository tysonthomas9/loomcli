package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

const (
	localDaemonPollInterval = 2 * time.Second
	localDaemonMaxBackoff   = 30 * time.Second
	localDaemonStopTimeout  = 12 * time.Second
)

var (
	localDaemonLoadConfig        = loadLocalDaemonConfigForWorkspace
	localDaemonWorkspaceHasRepos = workspaceHasReposForLocalDaemon
	localDaemonWorkspaceKeyFn    = localDaemonWorkspaceKey
)

func startLocalDaemonSupervisor(ctx context.Context, dataDir, exe string, port int, runtimeURL string) <-chan struct{} {
	done := make(chan struct{})
	go superviseLocalDaemon(ctx, dataDir, exe, port, runtimeURL, done)
	return done
}

func awaitLocalDaemonSupervisor(dataDir string, done <-chan struct{}) {
	select {
	case <-done:
	case <-time.After(localDaemonStopTimeout):
		appendLocalDaemonLog(dataDir, "managed daemon supervisor did not stop within "+localDaemonStopTimeout.String())
	}
}

func superviseLocalDaemon(ctx context.Context, dataDir, exe string, port int, runtimeURL string, done chan<- struct{}) {
	defer close(done)
	backoff := time.Second
	for {
		workspaceKey, runnable, err := localDaemonRunnableWorkspace(ctx, dataDir, runtimeURL)
		if err != nil {
			appendLocalDaemonLog(dataDir, "waiting for daemon config: "+err.Error())
		}
		if ctx.Err() != nil {
			return
		}
		if !runnable {
			backoff = time.Second
			if !sleepOrDone(ctx, localDaemonPollInterval) {
				return
			}
			continue
		}

		if err := runLocalDaemonOnce(ctx, dataDir, exe, port, workspaceKey); err != nil && ctx.Err() == nil {
			appendLocalDaemonLog(dataDir, "daemon exited: "+err.Error())
		}
		if ctx.Err() != nil {
			return
		}
		if !sleepOrDone(ctx, backoff) {
			return
		}
		backoff *= 2
		if backoff > localDaemonMaxBackoff {
			backoff = localDaemonMaxBackoff
		}
	}
}

func localDaemonRunnableWorkspace(ctx context.Context, dataDir, runtimeURL string) (string, bool, error) {
	workspaceKey, err := localDaemonWorkspaceKeyFn()
	if err != nil || workspaceKey == "" {
		return workspaceKey, false, err
	}
	hasRepos, err := localDaemonWorkspaceHasRepos(ctx, runtimeURL, workspaceKey)
	if err != nil {
		return workspaceKey, false, err
	}
	if hasRepos {
		return workspaceKey, true, nil
	}
	config, err := localDaemonLoadConfig(dataDir, workspaceKey)
	if err != nil {
		return workspaceKey, false, err
	}
	for _, agent := range config.Agents {
		if agent.ShouldSuperviseWithRoles(config.Roles) {
			return workspaceKey, true, nil
		}
	}
	return workspaceKey, false, nil
}

func loadLocalDaemonConfigForWorkspace(dataDir, workspaceKey string) (*cfgpkg.DaemonConfig, error) {
	previousWorkspace, hadWorkspace := os.LookupEnv("LOOM_WORKSPACE")
	if err := os.Setenv("LOOM_WORKSPACE", workspaceKey); err != nil {
		return nil, err
	}
	defer func() {
		if hadWorkspace {
			_ = os.Setenv("LOOM_WORKSPACE", previousWorkspace)
			return
		}
		_ = os.Unsetenv("LOOM_WORKSPACE")
	}()

	return cfgpkg.LoadDaemonConfig(dataDir)
}

func workspaceHasReposForLocalDaemon(ctx context.Context, runtimeURL, workspaceKey string) (bool, error) {
	if runtimeURL == "" {
		return false, fmt.Errorf("runtime URL is empty")
	}
	if workspaceKey == "" {
		return false, fmt.Errorf("workspace key is empty")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	endpoint := strings.TrimRight(runtimeURL, "/") + "/api/workspaces/" + url.PathEscape(workspaceKey) + "/repos"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("list workspace repos via runtime returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Repos []json.RawMessage `json:"repos"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, fmt.Errorf("decode workspace repos response: %w", err)
	}
	return len(payload.Repos) > 0, nil
}

func localDaemonWorkspaceKey() (string, error) {
	if workspaceKey := os.Getenv("LOOM_WORKSPACE"); workspaceKey != "" {
		return workspaceKey, nil
	}
	state, err := bootstrap.LoadStateCache()
	if err != nil {
		return "", err
	}
	return state.LastWorkspace, nil
}

func runLocalDaemonOnce(ctx context.Context, dataDir, exe string, port int, workspaceKey string) error {
	logFile, err := os.OpenFile(daemonLogPath(dataDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	cmd, err := loomReexecCommand(exe, "daemon")
	if err != nil {
		return err
	}
	cmd.Dir = dataDir
	cmd.Env = append(localEnv(dataDir, port), "LOOM_WORKSPACE="+workspaceKey)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start loom daemon: %w", err)
	}
	if err := updateRuntimeDaemonPID(dataDir, os.Getpid(), cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("record loom daemon pid: %w", err)
	}
	defer clearRuntimeDaemonPID(dataDir, os.Getpid(), cmd.Process.Pid)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	workspaceTicker := time.NewTicker(localDaemonPollInterval)
	defer workspaceTicker.Stop()
	return waitManagedLocalDaemon(ctx, dataDir, workspaceKey, cmd, done, workspaceTicker.C)
}

func waitManagedLocalDaemon(
	ctx context.Context,
	dataDir string,
	workspaceKey string,
	cmd *exec.Cmd,
	done <-chan error,
	workspaceTicks <-chan time.Time,
) error {
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			stopManagedLocalDaemon(cmd, done)
			return ctx.Err()
		case <-workspaceTicks:
			currentWorkspace, err := localDaemonWorkspaceKeyFn()
			if err != nil {
				appendLocalDaemonLog(dataDir, "cannot refresh active daemon workspace: "+err.Error())
				continue
			}
			if currentWorkspace == workspaceKey {
				continue
			}
			appendLocalDaemonLog(dataDir, fmt.Sprintf("active workspace changed from %s to %s; rotating managed daemon", workspaceKey, currentWorkspace))
			stopManagedLocalDaemon(cmd, done)
			return nil
		}
	}
}

func stopManagedLocalDaemon(cmd *exec.Cmd, done <-chan error) {
	if cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
	}
}

// updateRuntimeDaemonPID records the managed daemon child only when the
// runtime file still belongs to this local-service process. The ownership
// check prevents a delayed child exit from modifying a successor runtime.
func updateRuntimeDaemonPID(dataDir string, servicePID, daemonPID int) error {
	info, err := readRuntime(dataDir)
	if err != nil {
		return err
	}
	if info.PID != servicePID {
		return fmt.Errorf("runtime service changed from pid %d to %d", servicePID, info.PID)
	}
	info.DaemonPID = daemonPID
	return writeRuntime(dataDir, info)
}

func clearRuntimeDaemonPID(dataDir string, servicePID, daemonPID int) {
	info, err := readRuntime(dataDir)
	if err != nil || info.PID != servicePID || info.DaemonPID != daemonPID {
		return
	}
	info.DaemonPID = 0
	_ = writeRuntime(dataDir, info)
}

func appendLocalDaemonLog(dataDir, message string) {
	logFile, err := os.OpenFile(daemonLogPath(dataDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer func() { _ = logFile.Close() }()
	_, _ = fmt.Fprintf(logFile, "%s %s\n", time.Now().UTC().Format(time.RFC3339), message)
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
