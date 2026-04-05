package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// runDaemonAgentStop stops a single agent via a two-phase sequence:
// yield → poll → SIGTERM fallback. With --force or --timeout 0, skips yield.
func runDaemonAgentStop(agentName string, force bool, yieldTimeout time.Duration) {
	socketPath, err := resolveControlSocketFromCwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// --force or --timeout 0: skip yield, SIGTERM directly
	if force || yieldTimeout == 0 {
		fmt.Printf("Force-stopping agent %q...\n", agentName)
		forceStopAgent(socketPath, agentName)
		return
	}

	// Phase 1: Request yield (cooperative preemption)
	if !requestYieldOrFallback(socketPath, agentName) {
		return // agent not running or force-stopped via fallback
	}

	fmt.Printf("Requesting graceful stop for agent %q...\n", agentName)

	// Phase 2: Poll until agent stops, then Phase 3: force-stop on timeout
	pollAndForceStop(socketPath, agentName, yieldTimeout)
}

// requestYieldOrFallback sends agent_yield and handles error cases.
// Returns true if yield succeeded and polling should begin.
func requestYieldOrFallback(socketPath, agentName string) bool {
	resp, err := sendDaemonControlRequestFull(socketPath, DaemonControlRequest{
		Operation: ctrlOpAgentYield,
		AgentName: agentName,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if resp.Success {
		return true
	}

	// agent_yield failed — check for "not running" / "already stopped"
	if strings.Contains(resp.Error, "not running") || strings.Contains(resp.Error, "already stopped") {
		fmt.Printf("Agent %q is not running.\n", agentName)
		return false
	}
	// "not found" — agent doesn't exist at all
	if strings.Contains(resp.Error, "not found") {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}
	// Other errors — fall through to force stop
	fmt.Fprintf(os.Stderr, "Warning: yield request failed: %s\n", resp.Error)
	fmt.Fprintf(os.Stderr, "Falling back to SIGTERM...\n")
	forceStopAgent(socketPath, agentName)
	return false
}

// pollAndForceStop polls agent_list until the agent stops or timeout expires,
// then falls back to forceful SIGTERM.
func pollAndForceStop(socketPath, agentName string, yieldTimeout time.Duration) {
	start := time.Now()
	deadline := start.Add(yieldTimeout)
	lastProgress := start
	for time.Now().Before(deadline) {
		if !isAgentRunningViaSocket(socketPath, agentName) {
			fmt.Printf("Agent %q stopped gracefully.\n", agentName)
			return
		}
		time.Sleep(2 * time.Second)
		if time.Since(lastProgress) >= 10*time.Second {
			elapsed := time.Since(start).Truncate(time.Second)
			fmt.Printf("  Still waiting... (%s)\n", elapsed)
			lastProgress = time.Now()
		}
	}

	fmt.Printf("Yield timeout (%s). Sending SIGTERM...\n", yieldTimeout.Truncate(time.Second))
	forceStopAgent(socketPath, agentName)
}

// forceStopAgent sends a forceful agent_stop (SIGTERM, no yield).
// Treats "not found" / "already stopped" as success — the agent may have
// exited on its own during the yield polling window.
func forceStopAgent(socketPath, agentName string) {
	resp, err := sendDaemonControlRequestFull(socketPath, DaemonControlRequest{
		Operation: ctrlOpAgentStop,
		AgentName: agentName,
		Force:     true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !resp.Success {
		if strings.Contains(resp.Error, "not found") || strings.Contains(resp.Error, "already stopped") {
			fmt.Printf("Agent %q stopped.\n", agentName)
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}
	fmt.Printf("Agent %q stopped.\n", agentName)
}

// isAgentRunningViaSocket checks if an agent is still running by querying agent_list.
func isAgentRunningViaSocket(socketPath, agentName string) bool {
	resp, err := sendDaemonControlRequest(socketPath, ctrlOpAgentList, "")
	if err != nil {
		return false // daemon unreachable, treat as not running
	}
	if !resp.Success || resp.Data == nil {
		return false
	}
	var entries []AgentListEntry
	if err := json.Unmarshal(resp.Data, &entries); err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name == agentName && e.Status == "running" {
			return true
		}
	}
	return false
}

// stopDaemonForce sends SIGKILL to the daemon process.
func stopDaemonForce(pid int) {
	fmt.Printf("Force-stopping daemon (PID %d)...\n", pid)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		fmt.Fprintf(os.Stderr, "Error: sending SIGKILL: %v\n", err)
		os.Exit(1)
	}
	time.Sleep(1 * time.Second)
	if !lockfile.IsProcessRunning(pid) {
		fmt.Println("Daemon killed.")
	} else {
		fmt.Fprintf(os.Stderr, "Warning: process %d may still be running.\n", pid)
	}
}

// stopDaemonGraceful sends SIGTERM and waits up to 90s with progress output.
func stopDaemonGraceful(pid int) {
	fmt.Printf("Stopping daemon (PID %d)...\n", pid)
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "Error: sending SIGTERM: %v\n", err)
		os.Exit(1)
	}

	deadline := time.Now().Add(90 * time.Second)
	start := time.Now()
	lastProgress := start
	for time.Now().Before(deadline) {
		if !lockfile.IsProcessRunning(pid) {
			fmt.Println("Daemon stopped.")
			return
		}
		time.Sleep(500 * time.Millisecond)
		if time.Since(lastProgress) >= 10*time.Second {
			elapsed := time.Since(start).Truncate(time.Second)
			fmt.Printf("  Still waiting... (%s)\n", elapsed)
			lastProgress = time.Now()
		}
	}

	fmt.Fprintf(os.Stderr, "Warning: daemon did not stop within 90 seconds\n")
	fmt.Fprintf(os.Stderr, "Try: loom daemon stop --force\n")
	os.Exit(1)
}

func runDaemonAgentStart(cmd *cobra.Command, args []string) {
	socketPath, err := resolveControlSocketFromCwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	resp, err := sendDaemonControlRequest(socketPath, ctrlOpAgentStart, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !resp.Success {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}
	fmt.Printf("Agent %q started.\n", args[0])
}

func runDaemonAgentRestart(cmd *cobra.Command, args []string) {
	socketPath, err := resolveControlSocketFromCwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	resp, err := sendDaemonControlRequest(socketPath, ctrlOpAgentRestart, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !resp.Success {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}
	fmt.Printf("Agent %q restarted.\n", args[0])
}

// resolveControlSocketFromCwd resolves the daemon control socket path from cwd.
func resolveControlSocketFromCwd() (string, error) {
	projectDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot determine working directory: %w", err)
	}

	config, err := LoadDaemonConfig(projectDir)
	if err != nil {
		// Fall back to default PID file path
		config = &DaemonConfig{
			Daemon: DaemonSettings{
				PIDFile: ".loom/daemon.pid",
			},
		}
	}

	return resolveDaemonSocketPath(projectDir, config.Daemon.PIDFile), nil
}
