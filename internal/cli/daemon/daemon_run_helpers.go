package daemon

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/events/otelexport"
)

// startStateUpdater runs a goroutine that periodically writes the daemon state file.
// Returns a channel that is closed when the updater exits.
func startStateUpdater(shutdown <-chan struct{}, stateFilePath string, startedAt time.Time, daemon *Daemon, maxRetries int) <-chan struct{} {
	done := make(chan struct{})
	daemon.sup.RegisterTick(supervisor.GoroutineStateUpdater)
	go func() {
		defer close(done)
		defer daemon.sup.RecoverAndSignal(supervisor.GoroutineStateUpdater)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-shutdown:
				return
			case <-ticker.C:
				if err := writeStateFile(stateFilePath, startedAt, daemon.Agents(), daemon.QuarantinedTasks(), maxRetries); err != nil {
					fmt.Printf("Warning: failed to update state file: %v\n", err)
				}
				daemon.sup.RecordTick(supervisor.GoroutineStateUpdater)
			}
		}
	}()
	return done
}

// initOTelExporter initializes the OTel exporter if configured and subscribes it to the bus.
// Returns nil if OTel is not enabled or initialization fails.
func initOTelExporter(config *config.DaemonConfig, eventBus *events.Bus) *otelexport.Exporter {
	if config.Daemon.OTel == nil || !config.Daemon.OTel.Enabled {
		return nil
	}

	otelCfg := otelexport.Config{
		Enabled:         true,
		Endpoint:        config.Daemon.OTel.Endpoint,
		Protocol:        config.Daemon.OTel.Protocol,
		ServiceName:     config.Daemon.OTel.ServiceName,
		SampleRate:      config.Daemon.OTel.SampleRate,
		FlushIntervalMs: config.Daemon.OTel.FlushIntervalMs,
		Traces:          config.Daemon.OTel.Traces,
		Metrics:         config.Daemon.OTel.Metrics,
	}.Resolved()

	exp, err := otelexport.New(otelCfg)
	if err != nil {
		log.Printf("warning: failed to initialize OTel exporter: %v (continuing without OTel)", err)
		return nil
	}

	eventBus.Subscribe(exp.HandleEvent)
	log.Printf("OTel exporter initialized: endpoint=%s traces=%v metrics=%v",
		otelCfg.Endpoint, otelCfg.TracesEnabled(), otelCfg.MetricsEnabled())
	return exp
}

// stopOTelExporter gracefully shuts down the OTel exporter with a timeout.
func stopOTelExporter(exp *otelexport.Exporter) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exp.Stop(ctx); err != nil {
		log.Printf("warning: OTel shutdown error: %v", err)
	}
}

// printDryRunInfo displays what would happen in dry-run mode.
// NOTE: If config.DaemonSettings gains secret fields (RedisURL),
// their values should be masked via config.SecretResolver.MaskSecrets() before printing.
func printDryRunInfo(config *config.DaemonConfig, pidFile, logDir, stateFile string) {
	fmt.Println("DRY RUN - No daemon will be started")
	fmt.Println("")
	fmt.Println("Configuration:")
	fmt.Printf("  PID file: %s\n", pidFile)
	fmt.Printf("  State file: %s\n", stateFile)
	fmt.Printf("  Log directory: %s\n", logDir)
	if config.Daemon.RestartPolicy.MaxRetries != nil {
		fmt.Printf("  Max retries: %d\n", *config.Daemon.RestartPolicy.MaxRetries)
	} else {
		fmt.Printf("  Max retries: 3 (default)\n")
	}
	if config.Daemon.RestartPolicy.BackoffInitial != nil {
		fmt.Printf("  Backoff initial: %ds\n", *config.Daemon.RestartPolicy.BackoffInitial)
	} else {
		fmt.Printf("  Backoff initial: 2s (default)\n")
	}
	if config.Daemon.RestartPolicy.BackoffMax != nil {
		fmt.Printf("  Backoff max: %ds\n", *config.Daemon.RestartPolicy.BackoffMax)
	} else {
		fmt.Printf("  Backoff max: 300s (default)\n")
	}
	if config.Daemon.MaxAgents != nil {
		fmt.Printf("  Max agents: %d\n", *config.Daemon.MaxAgents)
	} else {
		fmt.Printf("  Max agents: 20 (default)\n")
	}
	fmt.Println("")
	fmt.Println("Agents to supervise:")
	for _, a := range config.Agents {
		auto := "auto: true"
		if !a.AutoEnabled() {
			auto = "auto: DISABLED"
		}
		fmt.Printf("  - %s (role: %s, %s)\n", a.Worktree, a.Role, auto)
	}
	fmt.Println("")
	fmt.Println("Recommended systemd resource controls:")
	fmt.Println("  LimitNOFILE=65536      # file descriptor limit")
	fmt.Println("  MemoryMax=4G           # memory ceiling")
	fmt.Println("  CPUQuota=200%          # CPU limit (200% = 2 cores)")
	fmt.Println("  TasksMax=256           # max tasks (processes+threads)")
}

// ResolveDaemonPath delegates to supervisor.ResolveDaemonPath for daemon callers.
func ResolveDaemonPath(projectDir, path string) string {
	return supervisor.ResolveDaemonPath(projectDir, path)
}
