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
	"strings"
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

// NewFleetDBServer creates and starts a FleetDBServer.
func NewFleetDBServer(cfg FleetDBServerConfig, logger *slog.Logger) (*FleetDBServer, error) {
	if cfg.RedisURL == "" && !cfg.AutoStart {
		return nil, fmt.Errorf("fleet-db: either RedisURL or AutoStart must be set")
	}
	applyFleetDBDefaults(&cfg)

	mr, redisAddr, err := resolveRedis(cfg, logger)
	if err != nil {
		return nil, err
	}

	cleanupRedis := func() {
		if mr != nil {
			mr.Close()
		}
	}

	fleetDBPath, err := exec.LookPath(cfg.FleetDBBin)
	if err != nil {
		cleanupRedis()
		return nil, fmt.Errorf("fleet-db binary not found in PATH; build it with: cd ../fleet-db && go install ./cmd/fleet-db")
	}

	port, err := findFreePort()
	if err != nil {
		cleanupRedis()
		return nil, fmt.Errorf("fleet-db: failed to find free port: %w", err)
	}

	cmd, err := startFleetDBProcess(fleetDBPath, port, redisAddr, logger)
	if err != nil {
		cleanupRedis()
		return nil, err
	}

	healthURL := fmt.Sprintf("http://127.0.0.1:%d/readyz", port)
	if err := waitForHealth(healthURL, 10*time.Second); err != nil {
		killProcessGroup(cmd)
		cleanupRedis()
		return nil, fmt.Errorf("fleet-db subprocess did not become ready within 10s")
	}

	c, err := client.New(client.Config{
		Transport: client.TransportHTTP,
		ServerURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		Workspace: cfg.Workspace,
		Actor:     cfg.Actor,
		Timeout:   30 * time.Second,
	})
	if err != nil {
		killProcessGroup(cmd)
		cleanupRedis()
		return nil, fmt.Errorf("fleet-db: failed to create client: %w", err)
	}

	ensureWorkspace(c, cfg.Workspace, logger)

	adapter := newFleetClientAdapter(c, cfg.Workspace, logger)
	backend := newFleetDBBackend(adapter, logger)

	logger.Info("fleet-db: server started", "port", port, "workspace", cfg.Workspace)
	return &FleetDBServer{
		cfg:       cfg,
		backend:   backend,
		client:    c,
		miniRedis: mr,
		cmd:       cmd,
		httpPort:  port,
		logger:    logger,
	}, nil
}

// Backend returns the IssueTracker backed by fleet-db.
func (s *FleetDBServer) Backend() IssueTracker {
	return s.backend
}

// Stop gracefully shuts down the fleet-db subprocess and cleans up resources.
func (s *FleetDBServer) Stop() error {
	if s.client != nil {
		if err := s.client.Close(); err != nil {
			s.logger.Warn("fleet-db: client close warning", "error", err)
		}
	}

	if s.cmd != nil && s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM)

		done := make(chan error, 1)
		go func() { done <- s.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}

	if s.miniRedis != nil {
		s.miniRedis.Close()
	}

	s.logger.Info("fleet-db: shutdown complete")
	return nil
}

// --- Internal helpers ---

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
	if cfg.RedisURL == "" {
		mr, err := miniredis.Run()
		if err != nil {
			return nil, "", fmt.Errorf("fleet-db: failed to start miniredis: %w", err)
		}
		logger.Info("fleet-db: started embedded miniredis", "addr", mr.Addr())
		return mr, mr.Addr(), nil
	}
	addr := strings.TrimPrefix(cfg.RedisURL, "redis://")
	return nil, addr, nil
}

func startFleetDBProcess(binPath string, port int, redisAddr string, logger *slog.Logger) (*exec.Cmd, error) {
	cmd := exec.Command(binPath, //nolint:gosec // binPath is from exec.LookPath, not user input
		"--addr", fmt.Sprintf("127.0.0.1:%d", port),
		"--redis-addr", redisAddr,
		"--auth-enabled=false",
		"--authz-enabled=false",
		"--rpc-enabled=false",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = slogWriter{logger: logger, level: slog.LevelDebug}
	cmd.Stderr = slogWriter{logger: logger, level: slog.LevelWarn}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("fleet-db: failed to start subprocess: %w", err)
	}
	return cmd, nil
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	}
}

func ensureWorkspace(c client.Client, workspace string, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.CreateWorkspace(ctx, &client.CreateWorkspaceRequest{
		Key:  workspace,
		Name: workspace,
	})
	if err != nil {
		var ce *client.ClientError
		if errors.As(err, &ce) && ce.IsConflict() {
			return // workspace already exists
		}
		logger.Warn("fleet-db: workspace creation warning", "error", err)
	}
}

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

func waitForHealth(url string, timeout time.Duration) error {
	httpClient := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(url) //nolint:gosec // local subprocess health check, not user-controlled
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
