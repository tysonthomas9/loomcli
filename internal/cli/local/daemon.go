package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

const (
	localDaemonPollInterval = 2 * time.Second
	localDaemonMaxBackoff   = 30 * time.Second
)

var (
	localDaemonLoadConfig        = loadLocalDaemonConfigForWorkspace
	localDaemonWorkspaceHasRepos = workspaceHasReposForLocalDaemon
)

func startLocalDaemonSupervisor(ctx context.Context, dataDir, exe string, port int, runtimeURL string) {
	go superviseLocalDaemon(ctx, dataDir, exe, port, runtimeURL)
}

func superviseLocalDaemon(ctx context.Context, dataDir, exe string, port int, runtimeURL string) {
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
	workspaceKey, err := localDaemonWorkspaceKey()
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

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
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
		return ctx.Err()
	}
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
