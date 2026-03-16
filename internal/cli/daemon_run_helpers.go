package cli

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/events/otelexport"
)

// startStateUpdater runs a goroutine that periodically writes the daemon state file.
// Returns a channel that is closed when the updater exits.
func startStateUpdater(shutdown <-chan struct{}, stateFilePath string, startedAt time.Time, daemon *Daemon, maxRetries int) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-shutdown:
				return
			case <-ticker.C:
				if err := writeStateFile(stateFilePath, startedAt, daemon.Agents(), maxRetries); err != nil {
					fmt.Printf("Warning: failed to update state file: %v\n", err)
				}
			}
		}
	}()
	return done
}

// initOTelExporter initializes the OTel exporter if configured and subscribes it to the bus.
// Returns nil if OTel is not enabled or initialization fails.
func initOTelExporter(config *DaemonConfig, eventBus *events.Bus) *otelexport.Exporter {
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

// initFleetDBServer starts the fleet-db backend if enabled in config.
// Returns the server (for deferred Stop) or nil if not enabled.
// Returns an error if fleet-db is enabled but fails to start.
func initFleetDBServer(config *DaemonConfig) (*FleetDBServer, error) {
	fleetSettings := &FleetDBSettings{}
	if config.Daemon.FleetDB != nil {
		fleetSettings = config.Daemon.FleetDB
	}
	if !resolveFleetDBEnabled(fleetSettings) {
		return nil, nil
	}

	fleetCfg := resolveFleetDBConfig(&config.Daemon)
	if fleetCfg.RedisURL == "" {
		fleetCfg.AutoStart = true
	}
	fleetCfg.Actor = "loom"

	srv, err := NewFleetDBServer(fleetCfg, slog.Default())
	if err != nil {
		return nil, fmt.Errorf("starting fleet-db backend: %w", err)
	}
	setDefaultTracker(srv.Backend())
	log.Printf("fleet-db backend active (workspace: %s)", fleetCfg.Workspace)
	return srv, nil
}

// printDryRunInfo displays what would happen in dry-run mode.
// NOTE: If DaemonSettings gains secret fields (RedisURL, APIKey, JWTKey),
// their values should be masked via SecretResolver.MaskSecrets() before printing.
func printDryRunInfo(config *DaemonConfig, pidFile, logDir, stateFile string) {
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
		fmt.Printf("  - %s (role: %s, auto: %v)\n", a.Worktree, a.Role, a.Auto)
	}
	fmt.Println("")
	fmt.Println("Recommended systemd resource controls:")
	fmt.Println("  LimitNOFILE=65536      # file descriptor limit")
	fmt.Println("  MemoryMax=4G           # memory ceiling")
	fmt.Println("  CPUQuota=200%          # CPU limit (200% = 2 cores)")
	fmt.Println("  TasksMax=256           # max tasks (processes+threads)")
}
