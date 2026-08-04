// Package worker implements `loom worker`: the remote worker process that
// registers with a `loom serve` control plane and runs tasks over HTTP-backed
// lock, event and log interfaces, plus its `profile` and `service` subcommands
// for editing WorkerProfile and agent-service records in the active workspace.
// Registered into the root command by cmd/loom via a blank import.
package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

var (
	workerControlPlane string
	workerWorkspace    string
	workerAgent        string
	workerBackend      string
	workerInterval     int
	workerMaxTasks     int
	workerIdleTimeout  int
	workerParentID     string
)

var workerCmd = &cobra.Command{
	Use:     "worker",
	Short:   "Run a remote agent worker that connects to a control plane",
	GroupID: "agents",
	Long: `Run a remote agent worker that registers with a loom control plane
and executes tasks using HTTP-backed interfaces for lock files, events,
and log forwarding. Designed for containers or remote machines.

The worker:
  1. Registers with the control plane
  2. Sets up HTTP-backed lock bridge, event emitter, and log forwarder
  3. Runs the auto mode loop with remote interfaces
  4. Deregisters on shutdown

FLAGS
  --control-plane  Control plane URL (or LOOM_WORKER_CONTROL_PLANE)
  --workspace      Workspace name (or LOOM_WORKER_WORKSPACE)
  --agent          Agent name (or LOOM_WORKER_AGENT)
  --backend        AI backend to use (or LOOM_WORKER_BACKEND)

AUTHENTICATION
  Set LOOM_WORKER_TOKEN to the shared secret for control plane auth.

EXAMPLES
  loom worker \
    --control-plane http://loom.example.com:8080 \
    --workspace my-workspace \
    --agent agent-alpha \
    --backend claude`,
	Args: cobra.NoArgs,
	Run:  runWorker,
}

func init() {
	defaultControlPlane := os.Getenv("LOOM_WORKER_CONTROL_PLANE")
	defaultWorkspace := os.Getenv("LOOM_WORKER_WORKSPACE")
	defaultAgent := os.Getenv("LOOM_WORKER_AGENT")
	defaultBackend := os.Getenv("LOOM_WORKER_BACKEND")

	workerCmd.Flags().StringVar(&workerControlPlane, "control-plane", defaultControlPlane, "Control plane URL (env: LOOM_WORKER_CONTROL_PLANE)")
	workerCmd.Flags().StringVar(&workerWorkspace, "workspace", defaultWorkspace, "Workspace UUID or name (env: LOOM_WORKER_WORKSPACE)")
	workerCmd.Flags().StringVar(&workerAgent, "agent", defaultAgent, "Agent name (env: LOOM_WORKER_AGENT)")
	workerCmd.Flags().StringVar(&workerBackend, "backend", defaultBackend, "AI backend (env: LOOM_WORKER_BACKEND)")
	workerCmd.Flags().IntVarP(&workerInterval, "interval", "i", 30, "Polling interval in seconds when no tasks available")
	workerCmd.Flags().IntVarP(&workerMaxTasks, "max-tasks", "m", 0, "Maximum tasks to process (0 = unlimited)")
	workerCmd.Flags().IntVarP(&workerIdleTimeout, "idle-timeout", "t", 0, "Exit after N minutes with no tasks (0 = none)")
	workerCmd.Flags().StringVar(&workerParentID, "parent", "", "Filter tasks to descendants of this epic ID")

	initWorkerProfileCommands()
	initWorkerServiceCommands()
	cli.RegisterCommand(workerCmd)
}

// workerRegistration is the request/response for worker registration.
type workerRegistration struct {
	WorkerID  string `json:"worker_id"`
	Workspace string `json:"workspace"`
	Agent     string `json:"agent"`
	Backend   string `json:"backend"`
	Token     string `json:"token,omitempty"` // returned by control plane
}

func validateWorkerFlags() (string, string) {
	if workerControlPlane == "" {
		fmt.Fprintln(os.Stderr, "Error: --control-plane is required (or set LOOM_WORKER_CONTROL_PLANE)")
		os.Exit(1)
	}
	if workerWorkspace == "" {
		fmt.Fprintln(os.Stderr, "Error: --workspace is required (or set LOOM_WORKER_WORKSPACE)")
		os.Exit(1)
	}
	if workerAgent == "" {
		fmt.Fprintln(os.Stderr, "Error: --agent is required (or set LOOM_WORKER_AGENT)")
		os.Exit(1)
	}
	if workerBackend != "" {
		if err := cli.SetBackend(workerBackend); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting backend: %v\n", err)
			os.Exit(1)
		}
	}
	worktreePath, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
		os.Exit(1)
	}
	return os.Getenv("LOOM_WORKER_TOKEN"), worktreePath
}

// isUUIDFormat checks whether s is a valid UUID string.
func isUUIDFormat(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

// resolveWorkerWorkspace resolves a workspace name to its UUID if needed.
// If the value is already a valid UUID, it is returned as-is.
// If it's a name and local config is available, the name is resolved to a UUID.
// If resolution fails (no local config, unknown name), the value is returned as-is
// and the server will reject it at registration time.
func resolveWorkerWorkspace(workspace string) string {
	if isUUIDFormat(workspace) {
		return workspace
	}
	cfg, err := config.LoadConfig()
	if err != nil || cfg == nil {
		return workspace
	}
	if ws, ok := cfg.Workspaces[workspace]; ok && ws.ID != "" {
		slog.Info("resolved workspace name to UUID",
			"name", workspace, "id", ws.ID)
		return ws.ID
	}
	return workspace
}

func runWorker(cmd *cobra.Command, args []string) {
	workerToken, worktreePath := validateWorkerFlags()
	workerWorkspace = resolveWorkerWorkspace(workerWorkspace)
	printWorkerBanner(worktreePath)

	reg, authToken := registerAndGetToken(workerToken)
	lockBridge, eventEmitter, logForwarder := setupWorkerInterfaces(reg, authToken)

	shutdown := setupWorkerShutdown()

	if err := cli.AcquireLock(worktreePath, "worker", workerAgent); err != nil {
		fmt.Fprintf(os.Stderr, "Error acquiring lock: %v\n", err)
		deregisterWorker(workerControlPlane, authToken, reg.WorkerID)
		os.Exit(1)
	}
	defer func() { _ = cli.ReleaseLock(worktreePath) }()

	automode.RunAutoModeLoop(automode.AutoModeOptions{
		Interval: workerInterval, MaxTasks: workerMaxTasks, IdleTimeout: workerIdleTimeout,
		AgentType: "task", AgentName: workerAgent, WorktreePath: worktreePath,
		ParentID: workerParentID, LockBridge: lockBridge, EventEmitter: eventEmitter,
	}, shutdown)

	cleanupWorkerResources(logForwarder, eventEmitter, authToken, reg.WorkerID)
}

func printWorkerBanner(worktreePath string) {
	fmt.Println("=========================================")
	fmt.Println("LOOM WORKER (Remote Agent)")
	fmt.Printf("Control plane: %s\n", workerControlPlane)
	fmt.Printf("Workspace:     %s\n", workerWorkspace)
	fmt.Printf("Agent:         %s\n", workerAgent)
	fmt.Printf("Backend:       %s\n", workerBackend)
	fmt.Printf("Worktree:      %s\n", worktreePath)
	fmt.Println("=========================================")
	fmt.Println()
}

func registerAndGetToken(workerToken string) (*workerRegistration, string) {
	fmt.Println("[worker] Registering with control plane...")
	reg, err := registerWorker(workerControlPlane, workerToken, workerRegistration{
		Workspace: workerWorkspace, Agent: workerAgent, Backend: workerBackend,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to register with control plane: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[worker] Registered as worker %s\n", reg.WorkerID)
	authToken := workerToken
	if reg.Token != "" {
		authToken = reg.Token
	}
	return reg, authToken
}

func setupWorkerInterfaces(reg *workerRegistration, authToken string) (*cli.HTTPLockBridge, *automode.HTTPEventEmitter, *LogForwarder) {
	lockBridge := &cli.HTTPLockBridge{
		ControlPlaneURL: workerControlPlane, WorkerID: reg.WorkerID, Token: authToken,
	}
	eventEmitter := &automode.HTTPEventEmitter{
		ControlPlaneURL: workerControlPlane, WorkerID: reg.WorkerID, Token: authToken,
	}
	logForwarder := NewLogForwarder(workerControlPlane, reg.WorkerID, authToken)
	multiOut := io.MultiWriter(os.Stdout, logForwarder)
	log.SetOutput(multiOut)
	return lockBridge, eventEmitter, logForwarder
}

func setupWorkerShutdown() chan struct{} {
	shutdown := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		sig := <-sigChan
		signal.Stop(sigChan)
		fmt.Printf("\n[worker] Shutdown signal received (%v), stopping gracefully...\n", sig)
		close(shutdown)
	}()
	return shutdown
}

func cleanupWorkerResources(logForwarder *LogForwarder, eventEmitter *automode.HTTPEventEmitter, authToken, workerID string) {
	fmt.Println("[worker] Deregistering from control plane...")
	if err := logForwarder.Close(); err != nil {
		log.Printf("[worker] Warning: log forwarder close error: %v", err)
	}
	if err := eventEmitter.Close(); err != nil {
		log.Printf("[worker] Warning: event emitter close error: %v", err)
	}
	deregisterWorker(workerControlPlane, authToken, workerID)
	fmt.Println("[worker] Done.")
}

func registerWorker(controlPlaneURL, token string, req workerRegistration) (*workerRegistration, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal registration: %w", err)
	}

	url := controlPlaneURL + "/api/internal/workers/register"
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("control plane returned %d: %s", resp.StatusCode, string(body))
	}

	var reg workerRegistration
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		return nil, fmt.Errorf("decode registration response: %w", err)
	}
	return &reg, nil
}

func deregisterWorker(controlPlaneURL, token, workerID string) {
	url := fmt.Sprintf("%s/api/internal/workers/%s", controlPlaneURL, workerID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		log.Printf("[worker] Warning: failed to create deregistration request: %v", err)
		return
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[worker] Warning: failed to deregister: %v", err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		log.Printf("[worker] Warning: deregistration returned %d", resp.StatusCode)
	} else {
		fmt.Printf("[worker] Deregistered worker %s\n", workerID)
	}
}
