package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"syscall"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/tysonthomas9/fleet-db/pkg/client"
)

// FleetDBServer manages the full lifecycle of an embedded fleet-db backend:
// starts miniredis (dev) or connects to real Redis (prod), launches a fleet-db
// HTTP subprocess, and exposes an IssueTracker via the pkg/client HTTP transport.
type FleetDBServer struct {
	cfg       FleetDBServerConfig
	backend   *fleetDBBackend
	client    client.Client
	miniRedis *miniredis.Miniredis
	cmd       *exec.Cmd
	httpPort  int
	logger    *slog.Logger
}

// NewFleetDBServer creates and starts a FleetDBServer. It validates config,
// optionally starts embedded miniredis, launches the fleet-db subprocess,
// waits for readiness, creates a client, and wires up the adapter + backend.
func NewFleetDBServer(cfg FleetDBServerConfig, logger *slog.Logger) (*FleetDBServer, error) {
	// 1. Validate config
	if cfg.RedisURL == "" && !cfg.AutoStart {
		return nil, fmt.Errorf("fleet-db: either RedisURL or AutoStart must be set")
	}

	// 2. Apply defaults
	if cfg.Workspace == "" {
		cfg.Workspace = "default"
	}
	if cfg.Actor == "" {
		cfg.Actor = "loom"
	}
	if cfg.FleetDBBin == "" {
		cfg.FleetDBBin = "fleet-db"
	}

	s := &FleetDBServer{cfg: cfg, logger: logger}

	// 3. Resolve Redis address
	var redisAddr string
	if cfg.RedisURL == "" {
		mr, err := miniredis.Run()
		if err != nil {
			return nil, fmt.Errorf("fleet-db: failed to start miniredis: %w", err)
		}
		s.miniRedis = mr
		redisAddr = mr.Addr()
		logger.Info("fleet-db: started embedded miniredis", "addr", redisAddr)
	} else {
		redisAddr = cfg.RedisURL
		// Strip redis:// prefix if present
		if len(redisAddr) > 8 && redisAddr[:8] == "redis://" {
			redisAddr = redisAddr[8:]
		}
	}

	// 4. Find fleet-db binary
	fleetDBPath, err := exec.LookPath(cfg.FleetDBBin)
	if err != nil {
		s.cleanupMiniRedis()
		return nil, fmt.Errorf("fleet-db binary not found in PATH; build it with: cd ../fleet-db && go install ./cmd/fleet-db")
	}

	// 5. Find a free TCP port
	port, err := findFreePort()
	if err != nil {
		s.cleanupMiniRedis()
		return nil, fmt.Errorf("fleet-db: failed to find free port: %w", err)
	}
	s.httpPort = port

	// 6. Build and start subprocess
	cmd := exec.Command(fleetDBPath, //nolint:gosec // fleetDBPath is resolved via exec.LookPath
		"--addr", fmt.Sprintf("127.0.0.1:%d", port),
		"--redis-addr", redisAddr,
		"--auth-enabled=false",
		"--rpc-enabled=false",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = slogWriter{logger: logger, level: slog.LevelDebug}
	cmd.Stderr = slogWriter{logger: logger, level: slog.LevelWarn}
	if err := cmd.Start(); err != nil {
		s.cleanupMiniRedis()
		return nil, fmt.Errorf("fleet-db: failed to start subprocess: %w", err)
	}
	s.cmd = cmd

	// 7. Poll for readiness
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/readyz", port)
	if err := waitForHealth(healthURL, 10*time.Second); err != nil {
		s.killSubprocess()
		s.cleanupMiniRedis()
		return nil, fmt.Errorf("fleet-db subprocess did not become ready within 10s")
	}

	// 8. Create fleet-db client
	c, err := client.New(client.Config{
		Transport: client.TransportHTTP,
		ServerURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		Workspace: cfg.Workspace,
		Actor:     cfg.Actor,
		Timeout:   30 * time.Second,
	})
	if err != nil {
		s.killSubprocess()
		s.cleanupMiniRedis()
		return nil, fmt.Errorf("fleet-db: failed to create client: %w", err)
	}
	s.client = c

	// 9. Ensure workspace exists
	s.ensureWorkspace(cfg.Workspace)

	// 10. Create adapter and backend
	adapter := newFleetClientAdapter(c, cfg.Workspace, logger)
	backend := newFleetDBBackend(adapter, logger)
	s.backend = backend

	logger.Info("fleet-db: server started", "port", port, "workspace", cfg.Workspace)
	return s, nil
}

// ensureWorkspace creates the workspace if it doesn't already exist.
func (s *FleetDBServer) ensureWorkspace(workspace string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := s.client.CreateWorkspace(ctx, &client.CreateWorkspaceRequest{
		Key:  workspace,
		Name: workspace,
	})
	if err != nil {
		var ce *client.ClientError
		if errors.As(err, &ce) && ce.IsConflict() {
			return // workspace already exists
		}
		s.logger.Warn("fleet-db: workspace creation warning", "error", err)
	}
}

// Backend returns the IssueTracker backed by fleet-db.
func (s *FleetDBServer) Backend() IssueTracker {
	return s.backend
}

// Stop gracefully shuts down the fleet-db subprocess and embedded miniredis.
func (s *FleetDBServer) Stop() error {
	// Close client
	if s.client != nil {
		if err := s.client.Close(); err != nil {
			s.logger.Warn("fleet-db: client close warning", "error", err)
		}
	}

	// Send SIGTERM to process group
	if s.cmd != nil && s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM)

		// Wait for process exit with 5s timeout
		done := make(chan error, 1)
		go func() { done <- s.cmd.Wait() }()
		select {
		case <-done:
			// clean exit
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				s.logger.Warn("fleet-db: subprocess did not exit after SIGKILL")
			}
		}
	}

	s.cleanupMiniRedis()
	s.logger.Info("fleet-db: shutdown complete")
	return nil
}

// cleanupMiniRedis closes miniredis if it was started.
func (s *FleetDBServer) cleanupMiniRedis() {
	if s.miniRedis != nil {
		s.miniRedis.Close()
	}
}

// killSubprocess kills the fleet-db subprocess process group.
func (s *FleetDBServer) killSubprocess() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		_ = s.cmd.Wait()
	}
}

// findFreePort returns an available TCP port on localhost.
func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		return 0, fmt.Errorf("close listener: %w", err)
	}
	return port, nil
}

// waitForHealth polls the given URL until it returns 200 or the timeout expires.
func waitForHealth(url string, timeout time.Duration) error {
	httpClient := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(url)
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

// slogWriter adapts slog.Logger to io.Writer for subprocess stdout/stderr.
type slogWriter struct {
	logger *slog.Logger
	level  slog.Level
}

func (w slogWriter) Write(p []byte) (int, error) {
	w.logger.Log(context.Background(), w.level, "fleet-db", "output", string(p))
	return len(p), nil
}

// Verify slogWriter satisfies io.Writer at compile time.
var _ io.Writer = slogWriter{}
