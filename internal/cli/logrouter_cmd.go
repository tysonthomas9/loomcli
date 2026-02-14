package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tysonthomas9/loomcli/internal/logrouter"
)

var (
	logRouterAgent      string
	logRouterBaseDir    string
	logRouterLockPath   string
	logRouterMaxLogSize int
)

var logRouterCmd = &cobra.Command{
	Use:    "log-router",
	Short:  "Route stdin to agent and task log files (internal)",
	Hidden: true,
	// Override root's PersistentPreRunE — log-router doesn't need backend resolution
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
	RunE: runLogRouter,
}

func init() {
	logRouterCmd.Flags().StringVar(&logRouterAgent, "agent", "", "Agent name for naming the agent log file (required)")
	logRouterCmd.Flags().StringVar(&logRouterBaseDir, "base-dir", "", "Base directory for log files (default: ~/.loom/logs)")
	logRouterCmd.Flags().StringVar(&logRouterLockPath, "lock-path", "", "Path to the .agent.lock file to watch for TaskID changes (required)")
	logRouterCmd.Flags().IntVar(&logRouterMaxLogSize, "max-log-size", 50, "Maximum log file size in MB before rotation (0 to disable)")
	_ = logRouterCmd.MarkFlagRequired("agent")
	_ = logRouterCmd.MarkFlagRequired("lock-path")
	rootCmd.AddCommand(logRouterCmd)
}

func runLogRouter(cmd *cobra.Command, args []string) error {
	baseDir := logRouterBaseDir
	if baseDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("getting home directory: %w", err)
		}
		baseDir = filepath.Join(homeDir, ".loom", "logs")
	}

	agentLogDir := filepath.Join(baseDir, "agents")
	if err := os.MkdirAll(agentLogDir, 0700); err != nil {
		return fmt.Errorf("creating agent log directory: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		cancel()
	}()

	maxLogSizeBytes := int64(logRouterMaxLogSize) * 1024 * 1024

	router, err := logrouter.NewLogRouter(logRouterAgent, baseDir, maxLogSizeBytes)
	if err != nil {
		return fmt.Errorf("creating log router: %w", err)
	}
	defer router.Close()

	watcher, err := logrouter.NewLockWatcher(logRouterLockPath, router)
	if err != nil {
		return fmt.Errorf("creating lock watcher: %w", err)
	}
	defer watcher.Close()

	go watcher.Watch(ctx)

	if err := router.RouteStdin(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("routing stdin: %w", err)
	}
	return nil
}
