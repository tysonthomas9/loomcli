package local

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func runTerminalHost(cmd *cobra.Command, _ []string) error {
	ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	dataDir, socketPath, err := terminalHostRuntimePaths()
	if err != nil {
		return err
	}
	info := newTerminalHostInfo(dataDir, socketPath)
	if err := writeTerminalHostInfo(dataDir, info); err != nil {
		return err
	}
	defer markTerminalHostStopped(dataDir, info)

	command := fmt.Sprintf("loom lead --backend %s", cli.ResolveBackendName())
	server := terminal.NewTerminalHostServer(socketPath, command, 0, slog.Default())
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Loom terminal host: %s\n", socketPath)
	if err := server.Serve(ctx); err != nil {
		info.Status = "failed"
		info.Healthy = false
		info.Error = err.Error()
		_ = writeTerminalHostInfo(dataDir, info)
		return err
	}
	return nil
}

func terminalHostRuntimePaths() (string, string, error) {
	dataDir, err := resolveDataDir(dataDirFlag)
	if err != nil {
		return "", "", err
	}
	if err := ensureRuntimeDirs(dataDir); err != nil {
		return "", "", err
	}
	socketPath := socketFlag
	if socketPath == "" {
		socketPath = terminalHostSocketPath(dataDir)
	}
	_ = os.Setenv("LOOM_CONFIG_DIR", dataDir)
	_ = os.Setenv("LOOM_DESKTOP_DATA_DIR", dataDir)
	_ = os.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", dataDir)
	return dataDir, socketPath, nil
}

func newTerminalHostInfo(dataDir, socketPath string) *terminalHostInfo {
	return &terminalHostInfo{
		ProtocolVersion: terminal.TerminalHostProtocolVersion,
		Status:          "running",
		PID:             os.Getpid(),
		DataDir:         dataDir,
		SocketPath:      socketPath,
		Build:           cli.Build,
		StartedAt:       time.Now().UTC(),
		Healthy:         true,
	}
}

func markTerminalHostStopped(dataDir string, info *terminalHostInfo) {
	if info.Status != "failed" {
		info.Status = "stopped"
		info.Error = ""
	}
	info.Healthy = false
	_ = writeTerminalHostInfo(dataDir, info)
}

func ensureTerminalHostRunning(cfg *localServiceConfig) (*terminalHostInfo, error) {
	socketPath := terminalHostSocketPath(cfg.dataDir)
	if info, err := readTerminalHostInfo(cfg.dataDir); err == nil {
		if terminalHostReusable(info, socketPath) {
			return info, nil
		}
		if processRunning(info.PID) {
			if err := stopTerminalHostWithInfo(info, 10*time.Second); err != nil {
				return nil, fmt.Errorf("stop incompatible terminal host: %w", err)
			}
		}
	}

	if err := spawnDetachedTerminalHost(cfg, socketPath); err != nil {
		return nil, err
	}
	if err := waitForTerminalHost(context.Background(), socketPath, 5*time.Second); err != nil {
		return nil, err
	}
	info, err := readTerminalHostInfo(cfg.dataDir)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func terminalHostReusable(info *terminalHostInfo, socketPath string) bool {
	if info == nil || info.ProtocolVersion != terminal.TerminalHostProtocolVersion {
		return false
	}
	if info.SocketPath != socketPath || !processRunning(info.PID) {
		return false
	}
	return terminal.NewTerminalHostClient(socketPath, 0).Ping() == nil
}

func spawnDetachedTerminalHost(cfg *localServiceConfig, socketPath string) error {
	logFile, err := os.OpenFile(terminalHostLogPath(cfg.dataDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open terminal host log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	hostCmd, err := loomReexecCommand(cfg.exe, "local",
		"--data-dir", cfg.dataDir,
		"terminal-host",
		"--socket", socketPath,
	)
	if err != nil {
		return err
	}
	configureDetachedCmd(hostCmd, cfg, logFile)
	if err := hostCmd.Start(); err != nil {
		return fmt.Errorf("start terminal host: %w", err)
	}
	if err := hostCmd.Process.Release(); err != nil {
		return fmt.Errorf("release terminal host process: %w", err)
	}
	return nil
}

func waitForTerminalHost(ctx context.Context, socketPath string, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := terminal.NewTerminalHostClient(socketPath, 0)
	if err := waitUntilHealthy(waitCtx, 100*time.Millisecond, func(context.Context) error {
		return client.Ping()
	}); err != nil {
		return fmt.Errorf("terminal host did not become healthy: %w", err)
	}
	return nil
}

func stopTerminalHost(dataDir string, timeout time.Duration) error {
	info, err := readTerminalHostInfo(dataDir)
	if err != nil {
		return nil
	}
	return stopTerminalHostWithInfo(info, timeout)
}

func stopTerminalHostWithInfo(info *terminalHostInfo, timeout time.Duration) error {
	if info == nil || info.PID <= 0 || !processRunning(info.PID) {
		return nil
	}
	if err := stopRuntimePIDs([]int{info.PID}, timeout); err != nil {
		return err
	}
	_ = os.Remove(info.SocketPath)
	return nil
}
