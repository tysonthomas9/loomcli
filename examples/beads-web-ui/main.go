// Package main provides the web UI server for Beads.
//
// This server embeds the React frontend at compile time and serves it
// along with API endpoints for interacting with the beads daemon.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/steveyegge/beads/examples/beads-web-ui/daemon"
	"github.com/steveyegge/beads/internal/rpc"
)

const (
	defaultPort            = 8080
	defaultPoolSize        = 5
	defaultShutdownTimeout = 5 * time.Second
	defaultMaxPortAttempts = 10
)

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

func main() {
	// Parse command-line flags
	port := flag.Int("port", defaultPort, "HTTP server port")
	socket := flag.String("socket", "", "Path to beads daemon socket (default: auto-detect)")
	poolSize := flag.Int("pool-size", defaultPoolSize, "Connection pool size")
	corsEnabled := flag.Bool("cors", false, "Enable CORS for development")
	corsOrigin := flag.String("cors-origin", "", "CORS allowed origins (comma-separated, default: http://localhost:3000)")
	terminalCmd := flag.String("terminal-cmd", "loom lead", "Command to run in terminal sessions (env: BEADS_TERMINAL_CMD)")
	flag.Parse()

	// Check environment variables for port override
	if envPort := os.Getenv("BEADS_WEBUI_PORT"); envPort != "" {
		var envPortInt int
		if n, err := fmt.Sscanf(envPort, "%d", &envPortInt); err == nil && n == 1 && envPortInt > 0 {
			*port = envPortInt
		} else {
			log.Printf("Warning: invalid BEADS_WEBUI_PORT value %q, using default %d", envPort, *port)
		}
	}

	// Check environment variables for CORS
	if os.Getenv("BEADS_WEBUI_CORS_ENABLED") == "true" {
		*corsEnabled = true
	}
	if envCorsOrigin := os.Getenv("BEADS_WEBUI_CORS_ORIGIN"); envCorsOrigin != "" {
		*corsOrigin = envCorsOrigin
	}
	if envTermCmd := os.Getenv("BEADS_TERMINAL_CMD"); envTermCmd != "" {
		*terminalCmd = envTermCmd
	}

	// Build CORS configuration
	corsConfig := CORSConfig{
		Enabled: *corsEnabled,
	}
	if *corsEnabled {
		if *corsOrigin != "" {
			// Parse comma-separated origins
			for _, origin := range strings.Split(*corsOrigin, ",") {
				origin = strings.TrimSpace(origin)
				if origin != "" {
					corsConfig.AllowedOrigins = append(corsConfig.AllowedOrigins, origin)
				}
			}
		} else {
			// Default to Vite dev server
			corsConfig.AllowedOrigins = []string{"http://localhost:3000"}
		}
	}

	// Log configuration
	log.Printf("Starting beads-web-ui server")
	log.Printf("Port: %d", *port)
	log.Printf("Connection pool size: %d", *poolSize)
	if *socket != "" {
		log.Printf("Daemon socket: %s", *socket)
	} else {
		log.Printf("Daemon socket: auto-detect")
	}
	if corsConfig.Enabled {
		log.Printf("CORS enabled for origins: %v", corsConfig.AllowedOrigins)
	}

	// Find an available port (auto-fallback if requested port is in use)
	listener, actualPort, err := findAvailablePort(*port, defaultMaxPortAttempts)
	if err != nil {
		log.Fatalf("Could not find available port: %v", err)
	}
	if actualPort != *port {
		log.Printf("Port %d in use, using port %d instead", *port, actualPort)
	}

	// Initialize daemon connection pool
	var pool *daemon.ConnectionPool
	var poolErr error

	if *socket != "" {
		// Use explicit socket path
		pool, poolErr = daemon.NewConnectionPool(*socket, *poolSize)
	} else {
		// Auto-discover daemon from current directory
		cwd, err := os.Getwd()
		if err != nil {
			log.Printf("Warning: failed to get current directory: %v", err)
		} else {
			pool, poolErr = daemon.NewConnectionPoolAutoDiscover(cwd, *poolSize)
		}
	}

	if poolErr != nil {
		log.Printf("Warning: failed to initialize daemon connection pool: %v", poolErr)
		log.Printf("The web UI will start but API endpoints may not work until a daemon is available")
	} else {
		log.Printf("Daemon connection pool initialized")
		// Test the connection
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			client, err := pool.Get(ctx)
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
	termMgr, err = NewTerminalManager(*terminalCmd, fmt.Sprintf("%d", actualPort))
	if err != nil {
		if errors.Is(err, ErrTmuxNotFound) {
			log.Printf("Warning: tmux not found, terminal feature disabled")
		} else {
			log.Printf("Warning: failed to initialize terminal manager: %v", err)
		}
		termMgr = nil
	}
	if termMgr != nil {
		log.Printf("Terminal manager initialized (default command: %s)", *terminalCmd)
	}

	// Create HTTP server
	mux := http.NewServeMux()
	setupRoutes(mux, pool, hub, getMutationsSince, termMgr, *terminalCmd)

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
	go func() {
		log.Printf("Listening on http://localhost:%d", actualPort)
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal for graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	signal.Stop(quit)

	log.Println("Shutting down server...")

	// Cancel the server-wide shutdown context so in-flight handlers' r.Context().Done()
	// fires immediately, causing them to abort quickly (e.g., pool.Get(r.Context()) fails fast).
	shutdownCancel()

	// Drain in-flight HTTP requests first (up to 5s, but most abort quickly due to cancelled context).
	// This ensures no handlers are running when we stop components below.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer drainCancel()

	if err := server.Shutdown(drainCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
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
}
