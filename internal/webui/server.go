// Package webui provides the web UI server for loomcli.
//
// This server embeds the React frontend at compile time and serves it
// along with API endpoints for interacting with the loomcli daemon.
package webui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

const (
	defaultPort            = 8080
	defaultPoolSize        = 5
	defaultShutdownTimeout = 5 * time.Second
	defaultMaxPortAttempts = 10
)

// ServerConfig holds configuration for the web UI server.
type ServerConfig struct {
	Port            int
	SocketPath      string
	PoolSize        int
	CORSEnabled     bool
	CORSOrigins     []string
	ShutdownTimeout time.Duration
	MaxPortAttempts int
	TerminalCmd     string
}

// DefaultConfig returns a ServerConfig with sensible defaults.
func DefaultConfig() ServerConfig {
	return ServerConfig{
		Port:            defaultPort,
		PoolSize:        defaultPoolSize,
		ShutdownTimeout: defaultShutdownTimeout,
		MaxPortAttempts: defaultMaxPortAttempts,
	}
}

// findAvailablePort attempts to find an available port starting from startPort.
// It tries up to maxAttempts consecutive ports and returns a listener on the first
// available port. The caller is responsible for closing the listener.
func findAvailablePort(startPort, maxAttempts int) (net.Listener, int, error) {
	for i := 0; i < maxAttempts; i++ {
		port := startPort + i
		addr := fmt.Sprintf(":%d", port)
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		// Return the listener open to avoid race conditions
		return listener, port, nil
	}
	return nil, 0, fmt.Errorf("no available port found in range %d-%d", startPort, startPort+maxAttempts-1)
}

// StartServer starts the web UI server with the given configuration.
// It blocks until the context is cancelled, then performs graceful shutdown.
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

	// Find an available port (auto-fallback if requested port is in use)
	listener, actualPort, err := findAvailablePort(config.Port, config.MaxPortAttempts)
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
	termMgr, err = NewTerminalManager(config.TerminalCmd, fmt.Sprintf("%d", actualPort))
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

	// Create HTTP server
	mux := http.NewServeMux()
	setupRoutes(mux, pool, hub, getMutationsSince, termMgr, config.TerminalCmd)

	// Wrap with CORS middleware if enabled
	corsMiddleware := NewCORSMiddleware(corsConfig)
	securityMiddleware := NewSecurityHeadersMiddleware()
	handler := h2c.NewHandler(securityMiddleware(corsMiddleware(mux)), &http2.Server{})

	// Create a shutdown context that all request contexts will derive from.
	// When cancelled, in-flight handlers' r.Context().Done() fires, causing
	// them to abort quickly rather than waiting the full drain timeout.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", actualPort),
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // Disabled: HTTP/2 streams (SSE, WebSocket) are long-lived; h2c handles flow control
		IdleTimeout:  60 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return shutdownCtx
		},
	}

	// Start server in a goroutine using the pre-acquired listener
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("Listening on http://localhost:%d", actualPort)
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

	// Drain in-flight HTTP requests first (up to ShutdownTimeout, but most abort quickly due to cancelled context).
	// This ensures no handlers are running when we stop components below.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	defer drainCancel()

	if err := server.Shutdown(drainCtx); err != nil {
		return fmt.Errorf("server forced to shutdown: %w", err)
	}
	log.Println("Server stopped")

	// Stop components in reverse-initialization order now that no handlers are running.

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
