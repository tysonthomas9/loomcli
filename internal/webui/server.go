// Package webui provides the web UI server for loomcli, embedding the React frontend
// at compile time and serving it along with API endpoints for the loomcli daemon.
package webui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

const (
	defaultPort            = 8080
	defaultPoolSize        = 100
	defaultShutdownTimeout = 5 * time.Second
	defaultMaxPortAttempts = 10
)

// ServerConfig holds configuration for the web UI server.
type ServerConfig struct {
	Port                    int
	BindAddress             string // Listen address (default: "127.0.0.1"; use "0.0.0.0" for all interfaces)
	SocketPath              string
	PoolSize                int
	CORSEnabled             bool
	CORSOrigins             []string
	ShutdownTimeout         time.Duration
	MaxPortAttempts         int
	TerminalCmd             string
	MaxTerminalSessions     int  // Maximum concurrent terminal connections (0 = default 20)
	FleetEnabled            bool // Register fleet API routes (requires Redis coordination)
	FleetRedis              *fleet.RedisConfig
	FleetJWTKey             []byte                         // Pre-provisioned JWT signing key for fleet auth (optional; if nil, server generates one)
	FleetAPIKey             string                         // Pre-shared API key for fleet worker registration (required for fleet register endpoint)
	APIKey                  string                         `json:"-"` // Pre-shared API key for WebUI auth (if empty and AuthEnabled, auto-generate)
	AuthEnabled             bool                           // Whether API authentication is enabled (default: true)
	HSTSEnabled             bool                           // Whether to send Strict-Transport-Security header (use when behind TLS-terminating proxy)
	LoomServerURL           string                         // Default target URL for the loom API proxy (set by 'loom serve')
	DevMode                 bool                           // Serve frontend from disk instead of embedded FS
	DevFrontendDir          string                         // Directory to serve in dev mode (default: internal/webui/frontend/dist)
	GitOps                  GitOps                         // Git operations interface (optional; nil disables git endpoints)
	FileOps                 FileOps                        // File operations interface (optional; nil disables file endpoints)
	WorkspaceConfigFn       func() (*WorkspaceData, error) // Workspace topology supplier; nil = single-repo mode
	WorkspaceDeleteFn       func(name string) error        // Workspace deletion function; nil = deletion unavailable
	SetDefaultWorkspaceFn   func(name string) error        // Set default workspace in config; nil = feature disabled
	ClearDefaultWorkspaceFn func() error                   // Clear default workspace in config; nil = feature disabled
	WorkspaceCreateFn       WorkspaceCreateFn              // Workspace creation function; nil = creation unavailable
	BackendOps              BackendOps                     // Backend health operations interface (optional; nil disables backend health endpoint)
	ScrollbackMaxLines      int                            // Maximum lines per scrollback buffer (0 = default 10000)
	SessionsStore           *sessions.Store                // File-based session audit trail store (optional; nil disables session endpoints)
	Logger                  *slog.Logger                   // Structured logger (optional; nil falls back to slog.Default())
}

// DefaultConfig returns a ServerConfig with sensible defaults.
func DefaultConfig() ServerConfig {
	return ServerConfig{
		Port:            defaultPort,
		BindAddress:     "127.0.0.1",
		PoolSize:        defaultPoolSize,
		ShutdownTimeout: defaultShutdownTimeout,
		MaxPortAttempts: defaultMaxPortAttempts,
	}
}

// findAvailablePort attempts to find an available port starting from startPort.
// It tries up to maxAttempts consecutive ports and returns a listener on the first
// available port. The caller is responsible for closing the listener.
func findAvailablePort(bindAddr string, startPort, maxAttempts int) (net.Listener, int, error) {
	for i := 0; i < maxAttempts; i++ {
		port := startPort + i
		addr := net.JoinHostPort(bindAddr, strconv.Itoa(port))
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		// Return the listener open to avoid race conditions
		return listener, port, nil
	}
	return nil, 0, fmt.Errorf("no available port found on %s in range %d-%d", bindAddr, startPort, startPort+maxAttempts-1)
}

// StartServer starts the web UI server with the given configuration.
// It blocks until the context is canceled, then performs graceful shutdown.
// Returns the actual port used (which may differ from config.Port if it was in use).
func StartServer(ctx context.Context, config ServerConfig) error {
	// Apply defaults for zero values
	if config.Port == 0 {
		config.Port = defaultPort
	}
	if config.PoolSize == 0 {
		config.PoolSize = defaultPoolSize
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = defaultShutdownTimeout
	}
	if config.MaxPortAttempts == 0 {
		config.MaxPortAttempts = defaultMaxPortAttempts
	}
	if config.BindAddress == "" {
		config.BindAddress = "127.0.0.1"
	}

	// Build CORS configuration
	corsConfig := CORSConfig{
		Enabled: config.CORSEnabled,
	}
	if config.CORSEnabled {
		if len(config.CORSOrigins) > 0 {
			corsConfig.AllowedOrigins = config.CORSOrigins
		} else {
			// Default to Vite dev server
			corsConfig.AllowedOrigins = []string{"http://localhost:3000"}
		}
	}

	// Log configuration
	slog.Info("starting web UI server", "port", config.Port, "pool_size", config.PoolSize, "bind_address", config.BindAddress)
	if config.SocketPath != "" {
		slog.Info("daemon socket configured", "socket", config.SocketPath)
	} else {
		slog.Info("daemon socket: auto-detect")
	}
	if corsConfig.Enabled {
		slog.Info("CORS enabled", "origins", corsConfig.AllowedOrigins)
	}
	if config.HSTSEnabled {
		slog.Info("HSTS enabled: ensure this server is behind a TLS-terminating proxy")
	}
	if config.DevMode {
		dir := config.DevFrontendDir
		if dir == "" {
			dir = "internal/webui/frontend/dist"
		}
		slog.Info("dev mode enabled", "frontend_dir", dir)
	}
	if config.FleetEnabled {
		slog.Info("fleet routes enabled")
	}

	// Find an available port (auto-fallback if requested port is in use)
	listener, actualPort, err := findAvailablePort(config.BindAddress, config.Port, config.MaxPortAttempts)
	if err != nil {
		return fmt.Errorf("could not find available port: %w", err)
	}
	if actualPort != config.Port {
		slog.Info("configured port in use, using fallback", "requested_port", config.Port, "actual_port", actualPort)
	}

	// Initialize MultiPool for workspace-aware connection routing.
	// The MultiPool implements daemon.Pool and dispatches Get calls to the
	// correct per-workspace ConnectionPool based on the workspace ID in context.
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, config.PoolSize)

	// Initialize the initial workspace connection pool (current project).
	// The workspace ID must match the current working directory's name because
	// the daemon pool connects to the local daemon socket. Using the default
	// workspace from config here would be wrong when a different workspace is
	// the default (e.g., after creating a second workspace).
	initialWorkspaceID := "default"
	if cwd, err := os.Getwd(); err == nil {
		initialWorkspaceID = filepath.Base(cwd)
	}
	var rawPool *daemon.ConnectionPool
	var pool daemon.Pool // may be ProtectedPool or raw ConnectionPool
	var poolErr error

	if config.SocketPath != "" {
		// Use explicit socket path
		rawPool, poolErr = daemon.NewConnectionPool(config.SocketPath, config.PoolSize)
	} else {
		// Auto-discover daemon from current directory
		cwd, err := getCwd()
		if err != nil {
			slog.Warn("failed to get current directory", "err", err)
		} else {
			rawPool, poolErr = daemon.NewConnectionPoolAutoDiscover(cwd, config.PoolSize)
		}
	}

	if poolErr != nil {
		slog.Warn("failed to initialize daemon connection pool", "err", poolErr)
		slog.Info("web UI will start but API endpoints may not work until daemon is available")
	} else {
		// Wrap pool with circuit breaker for resilience
		breaker := circuitbreaker.NewBreaker("daemon", circuitbreaker.Config{
			FailureThreshold:  5,
			OpenTimeout:       30 * time.Second,
			HalfOpenMaxProbes: 1,
			ShouldTrip:        daemon.DaemonShouldTrip,
			OnStateChange: func(from, to circuitbreaker.State) {
				slog.Info("circuit breaker state change", "component", "circuit_breaker", "from", from, "to", to)
			},
		})
		pool = daemon.NewProtectedPool(rawPool, breaker)
		slog.Info("daemon connection pool initialized with circuit breaker")

		// Register the initial workspace in the MultiPool
		if err := multiPool.Register(initialWorkspaceID, pool); err != nil {
			slog.Warn("failed to register initial workspace in MultiPool", "err", err)
		} else {
			slog.Info("registered initial workspace in MultiPool", "workspace", initialWorkspaceID)
		}

		// Test the connection
		func() {
			testCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			client, err := pool.Get(testCtx)
			if err != nil {
				slog.Warn("daemon not available at startup", "err", err)
				slog.Info("API endpoints will attempt to connect when called")
			} else {
				pool.Put(client)
				slog.Info("daemon connection verified")
			}
		}()
	}

	// Create SSE hub for real-time push notifications
	hub := NewSSEHub()
	go hub.Run()

	// Create multi-workspace subscriber to bridge mutations from per-workspace
	// daemons to SSE clients. Each workspace gets its own DaemonSubscriber
	// goroutine that tags mutations with the workspace ID.
	multiSub := NewMultiWorkspaceSubscriber(hub, multiPool, config.Logger)
	var getMutationsSince func(since int64) []rpc.MutationEvent
	if pool != nil {
		if err := multiSub.AddWorkspace(initialWorkspaceID); err != nil {
			slog.Warn("failed to add initial workspace subscriber", "workspace", initialWorkspaceID, "err", err)
		} else {
			slog.Info("workspace subscriber started", "workspace", initialWorkspaceID)
		}
		getMutationsSince = multiSub.GetMutationsSince
	}

	// Initialize terminal manager for WebSocket terminal sessions.
	// Include the workspace ID in the session prefix to prevent same-name agents
	// across workspaces from sharing tmux sessions.
	termSessionPrefix := fmt.Sprintf("%d-%s", actualPort, initialWorkspaceID)
	var termMgr *TerminalManager
	if termMgr, err = NewTerminalManager(config.TerminalCmd, termSessionPrefix, config.MaxTerminalSessions); err != nil {
		if errors.Is(err, ErrTmuxNotFound) {
			slog.Warn("tmux not found, terminal feature disabled")
		} else {
			slog.Warn("failed to initialize terminal manager", "err", err)
		}
	} else {
		if config.ScrollbackMaxLines > 0 {
			termMgr.SetScrollbackMaxLines(config.ScrollbackMaxLines)
		}
		slog.Info("terminal manager initialized", "component", "terminal", "default_command", config.TerminalCmd)
	}

	// Wire session history callback (deferred until sessionHistoryStore is initialized below).
	// We use a closure that captures a pointer so it can reference sessionHistoryStore
	// which is initialized later. The callback is only invoked after server startup,
	// by which point sessionHistoryStore is set.
	var sessionHistoryStoreRef *sessionhistory.Store
	if termMgr != nil {
		termMgr.SetOnSessionKilled(func(sessionName string) {
			store := sessionHistoryStoreRef
			if store == nil {
				return
			}
			issueID := extractIssueID(sessionName)
			if issueID == "" {
				return
			}
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return
			}
			scrollbackPath := homeDir + "/.loom/session-scrollback/" + sessionName + ".log"
			// Check if file exists before recording path.
			if _, err := os.Stat(scrollbackPath); err != nil {
				scrollbackPath = ""
			}
			if err := store.Complete(context.Background(), issueID, sessionName, scrollbackPath); err != nil {
				slog.Warn("failed to complete session history", "session", sessionName, "err", err)
			}
		})
	}

	// Initialize terminal auth for one-time WebSocket tokens
	var termAuth *terminalAuth
	if termMgr != nil {
		var authErr error
		termAuth, authErr = newTerminalAuth()
		if authErr != nil {
			slog.Warn("failed to initialize terminal auth, terminal feature disabled", "err", authErr)
			termMgr = nil
		}
	}

	// Initialize fleet store and JWT config for worker registration
	var fleetStore *fleet.Store
	var tokenCfg *TokenConfig
	if config.FleetRedis != nil {
		var err error
		fleetStore, err = fleet.NewStoreForWorkspace(*config.FleetRedis, initialWorkspaceID, nil)
		if err != nil {
			slog.Warn("failed to initialize fleet store", "component", "fleet", "err", err)
		} else {
			// Use pre-provisioned key if available, otherwise generate ephemeral key
			var jwtKey []byte
			if len(config.FleetJWTKey) > 0 {
				jwtKey = config.FleetJWTKey
				slog.Info("using pre-provisioned JWT signing key", "component", "fleet")
			} else {
				jwtKey = make([]byte, 32)
				if _, err := rand.Read(jwtKey); err != nil {
					slog.Warn("failed to generate JWT signing key", "component", "fleet", "err", err)
					_ = fleetStore.Close()
					fleetStore = nil
				}
			}
			if fleetStore != nil {
				tokenCfg = &TokenConfig{
					SigningKey: jwtKey,
					Expiry:     time.Hour,
				}
				slog.Info("fleet store initialized", "component", "fleet", "redis_address", config.FleetRedis.Address)
			}
		}
	}
	if fleetStore != nil {
		defer func() { _ = fleetStore.Close() }()
	}

	// Initialize fleet timeout enforcer
	var timeoutEnforcer *fleet.TimeoutEnforcer
	if fleetStore != nil {
		timeoutEnforcer = fleet.NewTimeoutEnforcer(fleetStore, fleet.DefaultTimeoutConfig(), nil)
		timeoutEnforcer.Start()
		slog.Info("fleet timeout enforcer started", "component", "fleet", "task_timeout", "30m")
	}

	// Initialize fleet claim metrics
	var claimMetrics *fleet.ClaimMetrics
	if fleetStore != nil {
		claimMetrics = fleet.NewClaimMetrics()
	}

	// Build fleet registration config (API key + rate limiter)
	var fleetRegCfg *FleetRegisterConfig
	if config.FleetAPIKey != "" && fleetStore != nil {
		fleetRegCfg = &FleetRegisterConfig{
			APIKey: config.FleetAPIKey,
		}
		// Create rate limiter using a separate Redis client
		if config.FleetRedis != nil {
			rlClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
			fleetRegCfg.RateLimiter = NewFleetRateLimiter(rlClient, 10, time.Minute)
			defer func() { _ = fleetRegCfg.RateLimiter.Close() }()
		}
		slog.Info("fleet API key authentication enabled", "component", "fleet")
	} else if fleetStore != nil && config.FleetAPIKey == "" {
		slog.Warn("fleet store configured but no fleet API key set", "component", "fleet", "env_var", "LOOM_FLEET_API_KEY")
		slog.Warn("fleet registration endpoint will return 503 until fleet API key is configured", "component", "fleet")
	}

	var tabMetaStore *tabmeta.Store
	if config.FleetRedis != nil {
		tmClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
		tabMetaStore = tabmeta.NewStore(tmClient, nil)
		defer func() { _ = tabMetaStore.Close() }()
		slog.Info("tab metadata store initialized", "redis_address", config.FleetRedis.Address)
		_ = tabMetaStore.MigrateLegacyKeys(ctx, DefaultWorkspace) // best-effort migration
	}

	// Initialize issue tab state store for tab persistence across navigation
	var issueTabStore *issuetabs.Store
	if config.FleetRedis != nil {
		itClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
		issueTabStore = issuetabs.NewStore(itClient, nil)
		defer func() { _ = issueTabStore.Close() }()
		slog.Info("issue tab store initialized", "redis_address", config.FleetRedis.Address)
	}

	// Initialize session history store for terminal session audit trail
	var sessionHistoryStore *sessionhistory.Store
	if config.FleetRedis != nil {
		shClient := fleet.NewRedisClient(config.FleetRedis.Address, config.FleetRedis.Password, 0)
		sessionHistoryStore = sessionhistory.NewStore(shClient, nil)
		defer func() { _ = sessionHistoryStore.Close() }()
		sessionHistoryStoreRef = sessionHistoryStore
		slog.Info("session history store initialized", "redis_address", config.FleetRedis.Address)
	}

	// Load or generate API key for authentication
	var apiKey string
	if config.AuthEnabled {
		if config.APIKey != "" {
			apiKey = config.APIKey
		} else {
			keyPath := DefaultAPIKeyPath()
			if keyPath != "" {
				var err error
				apiKey, err = LoadOrCreateAPIKey(keyPath)
				if err != nil {
					slog.Warn("failed to load/create API key, authentication disabled", "component", "auth", "err", err)
					config.AuthEnabled = false
				} else {
					slog.Info("API key loaded", "component", "auth", "path", keyPath)
				}
			} else {
				slog.Warn("cannot determine API key path, authentication disabled", "component", "auth")
				config.AuthEnabled = false
			}
		}
	}

	wrappedCreateFn := wrapWorkspaceCreateFn(config.WorkspaceCreateFn, multiPool, multiSub, config.PoolSize)

	// Create HTTP server and register routes (allowedOrigins: nil = same-origin only)
	mux := http.NewServeMux()
	clientErrLimiter, cspLimiter := setupRoutes(mux, pool, multiPool, hub, getMutationsSince, termMgr, termAuth, fleetStore, tokenCfg, apiKey, config.AuthEnabled, corsConfig.AllowedOrigins, fleetRegCfg, timeoutEnforcer, claimMetrics, config.FleetEnabled, config.DevMode, config.DevFrontendDir, config.LoomServerURL, config.GitOps, config.FileOps, tabMetaStore, issueTabStore, config.WorkspaceConfigFn, config.WorkspaceDeleteFn, config.SetDefaultWorkspaceFn, config.ClearDefaultWorkspaceFn, wrappedCreateFn, config.BackendOps, sessionHistoryStore, config.SessionsStore)
	defer clientErrLimiter.stop()
	defer cspLimiter.stop()

	registerWorkerAPIRoutes(mux, config.WorkspaceConfigFn)

	// Wrap with middleware chain: rate-limit -> security -> auth -> CORS -> mux
	// Rate limiting is outermost to reject floods before spending CPU on other middleware.
	// Auth sits between security headers and CORS so that:
	// - CORS preflight OPTIONS pass through without auth
	// - Security headers apply to all responses including 401s
	corsMiddleware := NewCORSMiddleware(corsConfig)
	authMiddleware := NewAuthMiddleware(AuthConfig{APIKey: apiKey, Enabled: config.AuthEnabled})
	securityMiddleware := NewSecurityHeadersMiddleware(SecurityConfig{HSTSEnabled: config.HSTSEnabled})
	rl, rateLimitMiddleware := NewRateLimitMiddleware(DefaultRateLimitConfig())
	handler := h2c.NewHandler(NewRequestLogMiddleware(config.Logger)(rateLimitMiddleware(securityMiddleware(authMiddleware(corsMiddleware(mux))))), &http2.Server{})

	// Create a shutdown context that all request contexts will derive from.
	// When canceled, in-flight handlers' r.Context().Done() fires, causing
	// them to abort quickly rather than waiting the full drain timeout.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	server := &http.Server{
		Addr:              net.JoinHostPort(config.BindAddress, strconv.Itoa(actualPort)),
		Handler:           handler,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second, // 30s default; streaming handlers (SSE, WebSocket) disable per-connection via ResponseController
		IdleTimeout:       60 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return shutdownCtx
		},
	}

	// Start server in a goroutine using the pre-acquired listener
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("server listening", "address", config.BindAddress, "port", actualPort)
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			serverErr <- fmt.Errorf("server error: %w", err)
		}
		close(serverErr)
	}()

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if err != nil {
			return err
		}
	}

	slog.Info("shutting down server")

	// Cancel the server-wide shutdown context so in-flight handlers' r.Context().Done()
	// fires immediately, causing them to abort quickly (e.g., pool.Get(r.Context()) fails fast).
	shutdownCancel()

	// Drain in-flight HTTP requests first (up to ShutdownTimeout, but most abort quickly due to canceled context).
	// This ensures no handlers are running when we stop components below.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	defer drainCancel()

	if err := server.Shutdown(drainCtx); err != nil {
		return fmt.Errorf("server forced to shutdown: %w", err)
	}
	slog.Info("server stopped")

	// Stop components in reverse-initialization order now that no handlers are running.

	// Stop fleet timeout enforcer
	if timeoutEnforcer != nil {
		timeoutEnforcer.Stop()
		slog.Info("fleet timeout enforcer stopped", "component", "fleet")
	}

	// Stop rate limiter cleanup goroutine
	rl.Stop()

	// Stop terminal auth cleanup goroutine
	if termAuth != nil {
		termAuth.Stop()
	}

	// Stop terminal manager (kill tmux sessions and close PTYs)
	if termMgr != nil {
		if err := termMgr.Shutdown(); err != nil {
			slog.Warn("error shutting down terminal manager", "component", "terminal", "err", err)
		} else {
			slog.Info("terminal manager stopped", "component", "terminal")
		}
	}

	// Stop multi-workspace subscriber (no more handlers need it)
	if multiSub != nil {
		multiSub.Stop()
		slog.Info("multi-workspace subscriber stopped")
	}

	// Stop SSE hub (all SSE handlers have exited)
	if hub != nil {
		hub.Stop()
		slog.Info("SSE hub stopped")
	}

	// Close MultiPool (closes all per-workspace pools including the initial one)
	if multiPool != nil {
		if err := multiPool.Close(); err != nil {
			slog.Warn("error closing multi-pool", "err", err)
		} else {
			slog.Info("multi-pool closed")
		}
	}

	return nil
}

// getCwd returns the current working directory.
func getCwd() (string, error) {
	return os.Getwd()
}
