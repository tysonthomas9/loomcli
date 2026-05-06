package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

const (
	localDaemonPollInterval = 2 * time.Second
	localDaemonMaxBackoff   = 30 * time.Second
)

func startLocalDaemonSupervisor(ctx context.Context, dataDir, exe string, port int) {
	go superviseLocalDaemon(ctx, dataDir, exe, port)
}

func superviseLocalDaemon(ctx context.Context, dataDir, exe string, port int) {
	backoff := time.Second
	for {
		workspaceKey, runnable, err := localDaemonRunnableWorkspace(dataDir)
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

func localDaemonRunnableWorkspace(dataDir string) (string, bool, error) {
	workspaceKey, err := localDaemonWorkspaceKey()
	if err != nil || workspaceKey == "" {
		return workspaceKey, false, err
	}
	config, err := loadLocalDaemonConfigForWorkspace(dataDir, workspaceKey)
	if err != nil {
		return workspaceKey, false, err
	}
	for _, agent := range config.Agents {
		if agent.ShouldSupervise() {
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
	logFile, err := os.OpenFile(daemonLogPath(dataDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(exe, "daemon") //nolint:gosec // runs this loom binary as the local agent supervisor
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
	logFile, err := os.OpenFile(daemonLogPath(dataDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
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
