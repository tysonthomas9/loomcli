package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"syscall"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/tysonthomas9/fleet-db/pkg/client"
)

// FleetDBServer manages the lifecycle of an embedded fleet-db backend.
// It starts an embedded miniredis (dev) or connects to real Redis (production),
// launches a fleet-db HTTP subprocess, and connects via the public pkg/client.
type FleetDBServer struct {
	cfg       FleetDBServerConfig
	backend   *fleetDBBackend
	client    client.Client
	miniRedis *miniredis.Miniredis
	cmd       *exec.Cmd
	httpPort  int
	logger    *slog.Logger
}

// NewFleetDBServer creates and starts a FleetDBServer.
//
// Startup sequence:
//  1. Validate config and apply defaults
//  2. Start miniredis or parse real Redis URL
//  3. Find fleet-db binary
//  4. Start fleet-db subprocess
//  5. Wait for readiness
//  6. Create client, ensure workspace, build adapter/backend
func NewFleetDBServer(cfg FleetDBServerConfig, logger *slog.Logger) (*FleetDBServer, error) { //nolint:gocognit // startup sequence is inherently sequential
	if cfg.RedisURL == "" && !cfg.AutoStart {
		return nil, fmt.Errorf("fleet-db: either RedisURL or AutoStart must be set")
	}
	applyFleetDBDefaults(&cfg)

	mr, redisAddr, err := resolveRedis(cfg, logger)
	if err != nil {
		return nil, err
	}

	fleetDBPath, err := exec.LookPath(cfg.FleetDBBin)
	if err != nil {
		closeMiniRedis(mr)
		return nil, fmt.Errorf("fleet-db binary not found in PATH; build it with: cd ../fleet-db && go install ./cmd/fleet-db")
	}

	port, err := findFreePort()
	if err != nil {
		closeMiniRedis(mr)
		return nil, fmt.Errorf("fleet-db: failed to find free port: %w", err)
	}

	cmd, err := startFleetDBProcess(fleetDBPath, port, redisAddr)
	if err != nil {
		closeMiniRedis(mr)
		return nil, err
	}
	logger.Info("fleet-db: subprocess started", "pid", cmd.Process.Pid, "port", port)

	readyzURL := fmt.Sprintf("http://127.0.0.1:%d/readyz", port)
	if err := waitForHealth(readyzURL, 10*time.Second); err != nil {
		killProcessGroup(cmd)
		closeMiniRedis(mr)
		return nil, fmt.Errorf("fleet-db subprocess did not become ready within 10s")
	}
	logger.Info("fleet-db: subprocess ready", "port", port)

	c, err := client.New(client.Config{
		Transport: client.TransportHTTP,
		ServerURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		Workspace: cfg.Workspace,
		Actor:     cfg.Actor,
		Timeout:   30 * time.Second,
	})
	if err != nil {
		killProcessGroup(cmd)
		closeMiniRedis(mr)
		return nil, fmt.Errorf("fleet-db: failed to create client: %w", err)
	}

	if err := ensureWorkspace(c, cfg.Workspace); err != nil {
		_ = c.Close()
		killProcessGroup(cmd)
		closeMiniRedis(mr)
		return nil, err
	}

	adapter := newFleetClientAdapter(c, cfg.Workspace, logger)
	backend := newFleetDBBackend(adapter, logger)

	return &FleetDBServer{
		cfg: cfg, backend: backend, client: c,
		miniRedis: mr, cmd: cmd, httpPort: port, logger: logger,
	}, nil
}

// Backend returns the IssueTracker backed by fleet-db.
func (s *FleetDBServer) Backend() IssueTracker {
	return s.backend
}

// Stop gracefully shuts down the fleet-db subprocess and related resources.
func (s *FleetDBServer) Stop() error {
	_ = s.client.Close()

	// Send SIGTERM to process group
	_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM)

	// Wait for process exit with timeout
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}

	closeMiniRedis(s.miniRedis)
	s.logger.Info("fleet-db: shutdown complete")
	return nil
}

// --- helpers ---

func applyFleetDBDefaults(cfg *FleetDBServerConfig) {
	if cfg.Workspace == "" {
		cfg.Workspace = "default"
	}
	if cfg.Actor == "" {
		cfg.Actor = "loom"
	}
	if cfg.FleetDBBin == "" {
		cfg.FleetDBBin = "fleet-db"
	}
}

func resolveRedis(cfg FleetDBServerConfig, logger *slog.Logger) (*miniredis.Miniredis, string, error) {
	if cfg.RedisURL != "" {
		return nil, stripRedisScheme(cfg.RedisURL), nil
	}
	mr, err := miniredis.Run()
	if err != nil {
		return nil, "", fmt.Errorf("fleet-db: failed to start miniredis: %w", err)
	}
	logger.Info("fleet-db: started embedded miniredis", "addr", mr.Addr())
	return mr, mr.Addr(), nil
}

func startFleetDBProcess(binPath string, port int, redisAddr string) (*exec.Cmd, error) {
	cmd := exec.Command(binPath, //nolint:gosec // binPath is resolved via exec.LookPath
		"--addr", fmt.Sprintf("127.0.0.1:%d", port),
		"--redis-addr", redisAddr,
		"--auth-enabled=false",
		"--authz-enabled=false",
		"--rpc-enabled=false",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("fleet-db: failed to start subprocess: %w", err)
	}
	return cmd, nil
}

func ensureWorkspace(c client.Client, workspace string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.CreateWorkspace(ctx, &client.CreateWorkspaceRequest{
		Key:  workspace,
		Name: workspace,
	})
	if err != nil {
		var ce *client.ClientError
		if errors.As(err, &ce) && ce.IsConflict() {
			return nil // workspace already exists
		}
		return fmt.Errorf("fleet-db: failed to create workspace %q: %w", workspace, err)
	}
	return nil
}

func killProcessGroup(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Wait()
}

func closeMiniRedis(mr *miniredis.Miniredis) {
	if mr != nil {
		mr.Close()
	}
}

// findFreePort binds a TCP listener to an ephemeral port and returns the port number.
func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// waitForHealth polls the given URL until it returns HTTP 200 or the timeout expires.
func waitForHealth(url string, timeout time.Duration) error {
	httpClient := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(url) //nolint:gosec // localhost health check URL constructed internally
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("fleet-db not ready after %v", timeout)
}

// stripRedisScheme removes a redis:// or rediss:// prefix from a URL, returning host:port.
func stripRedisScheme(rawURL string) string {
	for _, prefix := range []string{"rediss://", "redis://"} {
		if len(rawURL) > len(prefix) && rawURL[:len(prefix)] == prefix {
			return rawURL[len(prefix):]
		}
	}
	return rawURL
}
