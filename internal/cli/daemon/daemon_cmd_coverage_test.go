package daemon

import (
	"testing"
)

func TestPrintDryRunInfo_DefaultValues(t *testing.T) {
	config := &DaemonConfig{
		Agents: []AgentEntry{
			{Worktree: "alpha", Role: "plan", Auto: boolPtr(true)},
			{Worktree: "beta", Role: "task", Auto: boolPtr(false)},
		},
	}

	// Should not panic - exercises all default branches
	printDryRunInfo(config, "/tmp/pid", "/tmp/logs", "/tmp/state")
}

func TestPrintDryRunInfo_CustomValues(t *testing.T) {
	maxRetries := 5
	backoffInitial := 10
	backoffMax := 600
	maxAgents := 50

	config := &DaemonConfig{
		Daemon: DaemonSettings{
			RestartPolicy: RestartPolicy{
				MaxRetries:     &maxRetries,
				BackoffInitial: &backoffInitial,
				BackoffMax:     &backoffMax,
			},
			MaxAgents: &maxAgents,
		},
		Agents: []AgentEntry{
			{Worktree: "gamma", Role: "task", Auto: boolPtr(true)},
		},
	}

	// Should not panic - exercises all custom value branches
	printDryRunInfo(config, "/var/run/loom.pid", "/var/log/loom", "/var/run/loom.state")
}

func TestPrintDryRunInfo_NoAgents(t *testing.T) {
	config := &DaemonConfig{
		Agents: []AgentEntry{},
	}

	// Should not panic even with no agents
	printDryRunInfo(config, "/tmp/pid", "/tmp/logs", "/tmp/state")
}
