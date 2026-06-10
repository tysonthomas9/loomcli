package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
)

var (
	devProjectDir string
	devDistPath   string
)

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Run the Flue execution plane for a workflow project (supervised)",
	Long: `Starts the Flue dev server for a workflow project and supervises it:
crashes are restarted with backoff, and the runtime endpoint is
published so the loom daemon's workflow reconciler can attach.

The loom daemon (started separately) drives epic-runner wakes; this
command only owns the execution plane child.`,
	Args: cobra.NoArgs,
	RunE: runWorkflowDev,
}

func initDevFlags() {
	devCmd.Flags().StringVar(&devProjectDir, "project", "./workflows/examples/epic-runner", "Flue workflow project directory (source mode)")
	devCmd.Flags().StringVar(&devDistPath, "dist", "", "Built bundle (dist/server.mjs) to run instead of the project source")
}

func runWorkflowDev(_ *cobra.Command, _ []string) error {
	// Resolve fleet-db first: the Flue child needs its URL, and this
	// also boots the embedded fleet-db when nothing else is running.
	storeHandle, err := cmdstore.OpenStore(cmdstore.RootContext())
	if err != nil {
		return fmt.Errorf("open fleet-db store: %w", err)
	}
	defer func() { _ = storeHandle.Close() }()

	ws, err := bootstrap.ResolveActiveWorkspaceKey(cmdstore.RootContext(), nil)
	if err != nil {
		return fmt.Errorf("resolve active workspace: %w", err)
	}

	cfg := bootstrap.FlueConfig{
		Env: []string{
			"LOOM_FLEET_BASE_URL=" + storeHandle.URL(),
			"LOOM_WORKSPACE=" + ws,
		},
	}
	if devDistPath != "" {
		abs, err := filepath.Abs(devDistPath)
		if err != nil {
			return err
		}
		cfg.DistServerPath = abs
	} else {
		abs, err := filepath.Abs(devProjectDir)
		if err != nil {
			return err
		}
		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("workflow project %s: %w", abs, err)
		}
		cfg.ProjectDir = abs
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	backoff := 2 * time.Second
	const maxBackoff = 30 * time.Second
	for {
		flue, err := bootstrap.StartFlue(ctx, bootstrap.LoomDir(), cfg)
		if err != nil {
			if errors.Is(err, bootstrap.ErrFlueAlreadyRunning) || ctx.Err() != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "flue start failed: %v — retrying in %s\n", err, backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		fmt.Printf("Flue execution plane ready at %s (project: %s)\n", flue.URL(), cfg.ProjectDir+cfg.DistServerPath)
		fmt.Println("The loom daemon will attach automatically. Ctrl-C to stop.")
		backoff = 2 * time.Second

		exited := make(chan error, 1)
		go func() { exited <- flue.WaitExit() }()
		select {
		case <-ctx.Done():
			fmt.Println("\nStopping Flue…")
			return flue.Stop()
		case err := <-exited:
			_ = flue.Stop() // release lock + runtime.json
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(os.Stderr, "flue exited unexpectedly (%v) — restarting in %s\n", err, backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
		}
	}
}
