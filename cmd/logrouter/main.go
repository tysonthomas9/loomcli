// Package main provides the loom-router command that routes tmux pipe-pane output
// to multiple log destinations (agent log and task-specific logs).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	// Parse CLI flags
	agentName := flag.String("agent", "", "Agent name for naming the agent log file (required)")
	baseDir := flag.String("base-dir", "", "Base directory for log files (default: ~/.loom/logs)")
	lockPath := flag.String("lock-path", "", "Path to the .agent.lock file to watch for TaskID changes (required)")
	maxLogSizeMB := flag.Int("max-log-size", 50, "Maximum log file size in MB before rotation (0 to disable)")
	flag.Parse()

	// Validate required flags
	if *agentName == "" {
		fmt.Fprintln(os.Stderr, "error: --agent flag is required")
		flag.Usage()
		os.Exit(1)
	}
	if *lockPath == "" {
		fmt.Fprintln(os.Stderr, "error: --lock-path flag is required")
		flag.Usage()
		os.Exit(1)
	}

	// Set default base directory
	if *baseDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error getting home directory: %v\n", err)
			os.Exit(1)
		}
		*baseDir = filepath.Join(homeDir, ".loom", "logs")
	}

	// Create base directories
	agentLogDir := filepath.Join(*baseDir, "agents")
	if err := os.MkdirAll(agentLogDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "error creating agent log directory: %v\n", err)
		os.Exit(1)
	}

	// Set up context with cancellation for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Convert MB to bytes for the router
	maxLogSizeBytes := int64(*maxLogSizeMB) * 1024 * 1024

	// Create the router
	router, err := NewLogRouter(*agentName, *baseDir, maxLogSizeBytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating log router: %v\n", err)
		os.Exit(1)
	}
	defer router.Close()

	// Create and start the lock file watcher
	watcher, err := NewLockWatcher(*lockPath, router)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating lock watcher: %v\n", err)
		os.Exit(1)
	}
	defer watcher.Close()

	// Start watching in a goroutine
	go watcher.Watch(ctx)

	// Read from stdin and route to logs
	if err := router.RouteStdin(ctx); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "error routing stdin: %v\n", err)
		os.Exit(1)
	}
}
