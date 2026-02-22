// Package webui provides the web UI server for loomcli.
//
// This server embeds the React frontend at compile time and serves it
// along with API endpoints for interacting with the loomcli daemon.
package webui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

const (
	defaultPort            = 8080
	defaultPoolSize        = 100
	defaultShutdownTimeout = 5 * time.Second
	defaultMaxPortAttempts = 10
)

// ServerConfig holds configuration for the web UI server.
type ServerConfig struct {
	Port                int
	BindAddress         string // Listen address (default: "127.0.0.1"; use "0.0.0.0" for all interfaces)
	SocketPath          string
	PoolSize            int
	CORSEnabled         bool
	CORSOrigins         []string
	ShutdownTimeout     time.Duration
	MaxPortAttempts     int
	TerminalCmd         string
	MaxTerminalSessions int  // Maximum concurrent terminal connections (0 = default 20)
	FleetEnabled        bool // Register fleet API routes (requires Redis coordination)
	FleetRedis          *fleet.RedisConfig
	FleetJWTKey         []byte // Pre-provisioned JWT signing key for fleet auth (optional; if nil, server generates one)
	FleetAPIKey         string // Pre-shared API key for fleet worker registration (required for fleet register endpoint)
	APIKey              string // Pre-shared API key for WebUI auth (if empty and AuthEnabled, auto-generate)
	AuthEnabled         bool   // Whether API authentication is enabled (default: true)
	HSTSEnabled         bool   // Whether to send Strict-Transport-Security header (use when behind TLS-terminating proxy)
	LoomServerURL       string // Default target URL for the loom API proxy (set by 'loom serve')
	DevMode             bool   // Serve frontend from disk instead of embedded FS
	DevFrontendDir      string // Directory to serve in dev mode (default: internal/webui/frontend/dist)
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
	log.Printf("Starting loomcli web UI server")
	log.Printf("Port: %d", config.Port)
	log.Printf("Connection pool size: %d", config.PoolSize)
	if config.SocketPath != "" {
		log.Printf("Daemon socket: %s", config.SocketPath)
	} else {
		log.Printf("Daemon socket: auto-detect")
	}
	if corsConfig.Enabled {
		log.Printf("CORS enabled for origins: %v", corsConfig.AllowedOrigins)
	}
	if config.HSTSEnabled {
		log.Printf("HSTS enabled: ensure this server is behind a TLS-terminating proxy")
	}
	if config.DevMode {
		dir := config.DevFrontendDir
		if dir == "" {
			dir = "internal/webui/frontend/dist"
		}
		log.Printf("Dev mode: serving frontend from %s", dir)
	}
	if config.FleetEnabled {
		log.Printf("Fleet routes enabled")
	}

	// Find an available port (auto-fallback if requested port is in use)
	listener, actualPort, err := findAvailablePort(config.BindAddress, config.Port, config.MaxPortAttempts)
	if err != nil {
		return fmt.Errorf("could not find available port: %w", err)
	}
	if actualPort != config.Port {
		log.Printf("Port %d in use, using port %d instead", config.Port, actualPort)
	}

	// Initialize daemon connection pool
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
			log.Printf("Warning: failed to get current directory: %v", err)
		} else {
			rawPool, poolErr = daemon.NewConnectionPoolAutoDiscover(cwd, config.PoolSize)
		}
	}

	if poolErr != nil {
		log.Printf("Warning: failed to initialize daemon connection pool: %v", poolErr)
		log.Printf("The web UI will start but API endpoints may not work until a daemon is available")
	} else {
		// Wrap pool with circuit breaker for resilience
		breaker := circuitbreaker.NewBreaker("daemon", circuitbreaker.Config{
			FailureThreshold:  5,
			OpenTimeout:       30 * time.Second,
			HalfOpenMaxProbes: 1,
			ShouldTrip:        daemon.DaemonShouldTrip,
			OnStateChange: func(from, to circuitbreaker.State) {
				log.Printf("Circuit breaker state change: %s -> %s", from, to)
			},
		})
		pool = daemon.NewProtectedPool(rawPool, breaker)
		log.Printf("Daemon connection pool initialized with circuit breaker")

		// Test the connection
		func() {
			testCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			client, err := pool.Get(testCtx)
			if err != nil {
				log.Printf("Warning: daemon not available at startup: %v", err)
				log.Printf("API endpoints will attempt to connect when called")
			} else {
				pool.Put(client)
				log.Printf("Daemon connection verified")
			}
		}()
	}

	// Create SSE hub for real-time push notifications
	hub := NewSSEHub()
	go hub.Run()

	// Create daemon subscriber to bridge mutations from daemon to SSE clients
	var subscriber *DaemonSubscriber
	var getMutationsSince func(since int64) []rpc.MutationEvent
	if pool != nil {
		subscriber = NewDaemonSubscriber(pool, hub)
		subscriber.Start()
		getMutationsSince = subscriber.GetMutationsSince
		log.Printf("Daemon subscriber started")
	}

	// Initialize terminal manager for WebSocket terminal sessions
	var termMgr *TerminalManager
	termMgr, err = NewTerminalManager(config.TerminalCmd, fmt.Sprintf("%d", actualPort), config.MaxTerminalSessions)
	if err != nil {
		if errors.Is(err, ErrTmuxNotFound) {
			log.Printf("Warning: tmux not found, terminal feature disabled")
		} else {
			log.Printf("Warning: failed to initialize terminal manager: %v", err)
		}
		termMgr = nil
	}
	if termMgr != nil {
		log.Printf("Terminal manager initialized (default command: %s)", config.TerminalCmd)
	}

	// Initialize terminal auth for one-time WebSocket tokens
	var termAuth *terminalAuth
	if termMgr != nil {
		var authErr error
		termAuth, authErr = newTerminalAuth()
		if authErr != nil {
			log.Printf("Warning: failed to initialize terminal auth: %v", authErr)
			log.Printf("Terminal feature disabled (cannot ensure authenticated access)")
			termMgr = nil
		}
	}

	// Initialize fleet store and JWT config for worker registration
	var fleetStore *fleet.Store
	var tokenCfg *TokenConfig
	if config.FleetRedis != nil {
		var err error
		fleetStore, err = fleet.NewStore(*config.FleetRedis, nil)
		if err != nil {
			log.Printf("Warning: failed to initialize fleet store: %v", err)
		} else {
			// Use pre-provisioned key if available, otherwise generate ephemeral key
			var jwtKey []byte
			if len(config.FleetJWTKey) > 0 {
				jwtKey = config.FleetJWTKey
				log.Printf("Using pre-provisioned JWT signing key")
			} else {
				jwtKey = make([]byte, 32)
				if _, err := rand.Read(jwtKey); err != nil {
					log.Printf("Warning: failed to generate JWT signing key: %v", err)
					_ = fleetStore.Close()
					fleetStore = nil
				}
			}
			if fleetStore != nil {
				tokenCfg = &TokenConfig{
					SigningKey: jwtKey,
					Expiry:     time.Hour,
				}
				log.Printf("Fleet store initialized (Redis: %s)", config.FleetRedis.Address)
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
		log.Printf("Fleet timeout enforcer started (30min task timeout)")
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
		log.Printf("Fleet API key authentication enabled")
	} else if fleetStore != nil && config.FleetAPIKey == "" {
		log.Printf("Warning: fleet store configured but no fleet API key set (LOOM_FLEET_API_KEY)")
		log.Printf("Fleet registration endpoint will return 503 until a fleet API key is configured")
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
					log.Printf("Warning: failed to load/create API key: %v", err)
					log.Printf("Authentication will be disabled")
					config.AuthEnabled = false
				} else {
					log.Printf("API key loaded from %s", keyPath)
				}
			} else {
				log.Printf("Warning: cannot determine API key path, authentication disabled")
				config.AuthEnabled = false
			}
		}
	}

	// Create HTTP server
	mux := http.NewServeMux()
	// Pass allowed origins for WebSocket origin validation.
	// When CORS is disabled, nil origins means only same-origin connections are accepted.
	setupRoutes(mux, pool, hub, getMutationsSince, termMgr, termAuth, fleetStore, tokenCfg, apiKey, config.AuthEnabled, corsConfig.AllowedOrigins, fleetRegCfg, timeoutEnforcer, claimMetrics, config.FleetEnabled, config.DevMode, config.DevFrontendDir, config.LoomServerURL)

	// Wrap with middleware chain: rate-limit -> security -> auth -> CORS -> mux
	// Rate limiting is outermost to reject floods before spending CPU on other middleware.
	// Auth sits between security headers and CORS so that:
	// - CORS preflight OPTIONS pass through without auth
	// - Security headers apply to all responses including 401s
	corsMiddleware := NewCORSMiddleware(corsConfig)
	authMiddleware := NewAuthMiddleware(AuthConfig{APIKey: apiKey, Enabled: config.AuthEnabled})
	securityMiddleware := NewSecurityHeadersMiddleware(SecurityConfig{HSTSEnabled: config.HSTSEnabled})
	rl, rateLimitMiddleware := NewRateLimitMiddleware(DefaultRateLimitConfig())
	handler := h2c.NewHandler(rateLimitMiddleware(securityMiddleware(authMiddleware(corsMiddleware(mux)))), &http2.Server{})

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
		log.Printf("Listening on http://%s:%d", config.BindAddress, actualPort)
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

	log.Println("Shutting down server...")

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
	log.Println("Server stopped")

	// Stop components in reverse-initialization order now that no handlers are running.

	// Stop fleet timeout enforcer
	if timeoutEnforcer != nil {
		timeoutEnforcer.Stop()
		log.Printf("Fleet timeout enforcer stopped")
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
			log.Printf("Warning: error shutting down terminal manager: %v", err)
		} else {
			log.Printf("Terminal manager stopped")
		}
	}

	// Stop daemon subscriber (no more handlers need it)
	if subscriber != nil {
		subscriber.Stop()
		log.Printf("Daemon subscriber stopped")
	}

	// Stop SSE hub (all SSE handlers have exited)
	if hub != nil {
		hub.Stop()
		log.Printf("SSE hub stopped")
	}

	// Close daemon connection pool last (subscriber/hub may have used it)
	if pool != nil {
		if err := pool.Close(); err != nil {
			log.Printf("Warning: error closing connection pool: %v", err)
		} else {
			log.Printf("Daemon connection pool closed")
		}
	}

	return nil
}

// getCwd returns the current working directory.
func getCwd() (string, error) {
	return os.Getwd()
}
