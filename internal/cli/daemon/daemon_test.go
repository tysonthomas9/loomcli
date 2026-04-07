package daemon

// makeDaemonConfig creates a DaemonConfig with defaults for testing.
// Kept here because daemon_events_test.go, daemon_control_test.go, and
// daemon_ipc_test.go use it.
func makeDaemonConfig(agents []AgentEntry, roles map[string]RoleConfig) *DaemonConfig {
	return &DaemonConfig{
		Daemon: DaemonSettings{
			PIDFile: ".loom/daemon.pid",
			LogDir:  ".loom/logs",
			RestartPolicy: RestartPolicy{
				MaxRetries:     intPtr(3),
				BackoffInitial: intPtr(2),
				BackoffMax:     intPtr(300),
			},
		},
		Roles:  roles,
		Agents: agents,
	}
}
