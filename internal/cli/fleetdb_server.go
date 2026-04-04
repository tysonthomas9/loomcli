// Package cli — FleetDBServer manages the lifecycle of an embedded beads
// issue-tracking server with optional Redis-backed fleet coordination.
// It creates storage, starts an in-process RPC server, connects a client,
// and wraps everything in a fleetDBBackend for use as an IssueTracker.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	beads "github.com/steveyegge/beads"

	"github.com/tysonthomas9/loomcli/internal/backend"
	beadsbackend "github.com/tysonthomas9/loomcli/internal/backend/beads"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// maxUnixSocketPath is the maximum length for a Unix socket path on Linux.
const maxUnixSocketPath = 108

// FleetDBServerConfig holds configuration for FleetDBServer.
type FleetDBServerConfig struct {
	RedisURL   string // Redis connection URL. Empty = use miniredis if AutoStart.
	Workspace  string // Workspace/project identifier.
	AutoStart  bool   // If true and RedisURL empty, auto-start miniredis.
	DBPath     string // SQLite database path. Empty = in-memory storage.
	SocketPath string // Unix socket path for RPC server.
}

// FleetDBServer manages the lifecycle of an embedded beads issue-tracking
// server with optional Redis-backed fleet coordination.
type FleetDBServer struct {
	backend    backend.IssueBackend
	rpcServer  *beads.Server
	rpcClient  *rpc.Client
	rdb        *redis.Client
	miniRedis  *miniredis.Miniredis
	fleetStore *fleet.Store
	stopOnce   sync.Once
	logger     *slog.Logger
}

// NewFleetDBServer creates and starts a FleetDBServer with the given configuration.
func NewFleetDBServer(cfg FleetDBServerConfig, logger *slog.Logger) (*FleetDBServer, error) {
	if cfg.SocketPath == "" {
		return nil, fmt.Errorf("SocketPath is required")
	}
	if len(cfg.SocketPath) >= maxUnixSocketPath {
		return nil, fmt.Errorf("socket path too long (%d chars, max %d): use a shorter path", len(cfg.SocketPath), maxUnixSocketPath-1)
	}
	if cfg.Workspace == "" {
		cfg.Workspace = "default"
	}
	if logger == nil {
		logger = slog.Default()
	}

	srv := &FleetDBServer{logger: logger}

	// 1. Redis setup (optional fleet coordination)
	if cfg.RedisURL == "" && cfg.AutoStart {
		mr, err := miniredis.Run()
		if err != nil {
			return nil, fmt.Errorf("failed to start embedded redis: %w", err)
		}
		srv.miniRedis = mr
		cfg.RedisURL = "redis://" + mr.Addr()
	}

	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			srv.cleanup()
			return nil, fmt.Errorf("failed to parse redis URL: %w", err)
		}
		srv.rdb = redis.NewClient(opts)
		srv.fleetStore = fleet.NewStoreFromClient(srv.rdb, logger)
	}

	// 2. Storage
	var store beads.Storage
	var err error
	if cfg.DBPath != "" {
		store, err = beads.NewSQLiteStorage(context.Background(), cfg.DBPath)
		if err != nil {
			srv.cleanup()
			return nil, fmt.Errorf("failed to create sqlite storage: %w", err)
		}
	} else {
		store = beads.NewMemoryStorage("")
	}

	// 3. RPC server
	srv.rpcServer = beads.NewServer(cfg.SocketPath, store, cfg.Workspace, cfg.DBPath)

	serverErrCh := make(chan error, 1)
	go func() {
		if err := srv.rpcServer.Start(context.Background()); err != nil {
			serverErrCh <- err
		}
	}()

	// Wait for server to be ready or fail
	select {
	case <-srv.rpcServer.WaitReady():
		// Server is listening
	case err := <-serverErrCh:
		srv.cleanup()
		return nil, fmt.Errorf("failed to start RPC server: %w", err)
	case <-time.After(5 * time.Second):
		_ = srv.rpcServer.Stop()
		srv.cleanup()
		return nil, fmt.Errorf("RPC server did not become ready within 5s")
	}

	// 4. Connect RPC client
	client, err := rpc.TryConnectWithTimeout(cfg.SocketPath, 5*time.Second)
	if err != nil {
		_ = srv.rpcServer.Stop()
		srv.cleanup()
		return nil, fmt.Errorf("failed to connect to RPC server: %w", err)
	}
	if client == nil {
		_ = srv.rpcServer.Stop()
		srv.cleanup()
		return nil, fmt.Errorf("failed to connect to RPC server at %s: server may be unhealthy or health check failed", cfg.SocketPath)
	}
	srv.rpcClient = client

	// 5. Backend — use BeadsBackend (backend.IssueBackend) instead of legacy fleetDBBackend
	srv.backend = beadsbackend.New(client)

	return srv, nil
}

// Backend returns the issue-tracking backend.
func (s *FleetDBServer) Backend() backend.IssueBackend {
	return s.backend
}

// FleetStore returns the fleet coordination store (may be nil if Redis is not configured).
func (s *FleetDBServer) FleetStore() *fleet.Store {
	return s.fleetStore
}

// Stop gracefully shuts down all components in reverse startup order.
func (s *FleetDBServer) Stop() {
	s.stopOnce.Do(func() {
		if s.rpcClient != nil {
			_ = s.rpcClient.Close()
		}
		if s.rpcServer != nil {
			if err := s.rpcServer.Stop(); err != nil {
				s.logger.Warn("failed to stop RPC server", "error", err)
			}
		}
		s.cleanup()
	})
}

// cleanup releases Redis resources. Idempotent — safe to call multiple times
// or on partial initialization.
func (s *FleetDBServer) cleanup() {
	// fleetStore.Close() also closes the underlying rdb client, so skip
	// the separate rdb.Close() when fleetStore owns it.
	if s.fleetStore != nil {
		_ = s.fleetStore.Close()
		s.fleetStore = nil
		s.rdb = nil // owned by fleetStore
	} else if s.rdb != nil {
		_ = s.rdb.Close()
		s.rdb = nil
	}
	if s.miniRedis != nil {
		s.miniRedis.Close()
		s.miniRedis = nil
	}
}
