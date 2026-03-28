package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// runDaemonAgentStop stops a single agent via the daemon control socket.
func runDaemonAgentStop(agentName string) {
	socketPath, err := resolveControlSocketFromCwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	resp, err := sendDaemonControlRequest(socketPath, ctrlOpAgentStop, agentName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !resp.Success {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}
	fmt.Printf("Agent %q stopped.\n", agentName)
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
