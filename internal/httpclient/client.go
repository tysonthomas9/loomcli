// Package httpclient provides an authenticated HTTP client for CLI-to-server
// communication. It discovers the server's auth mode via GET /api/config and
// acquires a JWT via device-code flow when external auth is required.
package httpclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/authmode"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Config holds the parameters needed to create an authenticated client.
type Config struct {
	ServerURL string // Base URL of the loom server (e.g., "https://loom.example.com:8080")
	Actor     string // Configured X-Actor fallback; worker-specific env takes precedence.
}

// AuthMode represents the server's authentication configuration.
type AuthMode struct {
	Mode    string `json:"mode"`               // "open" or "oidc"
	AuthURL string `json:"auth_url,omitempty"` // Better Auth service URL (when mode is "oidc")
}

// Client wraps http.Client with automatic auth discovery and token injection.
type Client struct {
	serverURL   string
	authMode    *AuthMode
	token       string
	tokenExpiry time.Time
	actor       string
	httpClient  *http.Client
	mu          sync.Mutex
}

// New creates a new authenticated client. It calls GET /api/config to discover
// the auth mode. If the server requires external auth and no cached token is
// available, it initiates the device authorization flow (interactive).
func New(cfg Config) (*Client, error) {
	serverURL := strings.TrimRight(cfg.ServerURL, "/")

	c := &Client{
		serverURL: serverURL,
		actor:     bootstrap.ResolveFleetDBActor(cfg.Actor),
		httpClient: &http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
			Timeout:   30 * time.Second,
		},
	}

	authMode, err := c.discoverAuthMode()
	if err != nil {
		return nil, err
	}
	c.authMode = authMode

	if authMode.Mode == authmode.ModeOIDC {
		if err := c.ensureToken(); err != nil {
			return nil, err
		}
	}

	return c, nil
}

// Do executes an HTTP request with the appropriate Authorization header.
// If the token has expired, it re-authenticates before sending.
// On 401 response, it clears the cached token, re-authenticates, and retries once.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c.actor != "" {
		req.Header.Set("X-Actor", c.actor)
	}
	c.mu.Lock()
	if c.authMode.Mode == authmode.ModeOIDC {
		if err := c.ensureToken(); err != nil {
			c.mu.Unlock()
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	c.mu.Unlock()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	// On 401, clear cache and retry once.
	if resp.StatusCode == http.StatusUnauthorized && c.authMode.Mode == authmode.ModeOIDC {
		resp.Body.Close()

		// Reset request body for retry. If the original request had a body
		// and GetBody is not set, we cannot safely replay it.
		if req.Body != nil && req.GetBody == nil {
			return nil, fmt.Errorf("re-authentication required but request body cannot be replayed (set GetBody)")
		}
		if req.GetBody != nil {
			req.Body, err = req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("re-authentication: resetting request body: %w", err)
			}
		}

		c.mu.Lock()
		_ = clearCachedToken(c.serverURL)
		c.token = ""
		c.tokenExpiry = time.Time{}
		if err := c.authenticate(); err != nil {
			c.mu.Unlock()
			return nil, fmt.Errorf("re-authentication failed: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		c.mu.Unlock()

		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// Close cleans up any resources.
func (c *Client) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// discoverAuthMode calls GET {serverURL}/api/config to determine the auth mode.
func (c *Client) discoverAuthMode() (*AuthMode, error) {
	resp, err := c.httpClient.Get(c.serverURL + "/api/config")
	if err != nil {
		return nil, fmt.Errorf("cannot reach loom server at %s: %w", c.serverURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("reading /api/config response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cannot reach loom server at %s: HTTP %d", c.serverURL, resp.StatusCode)
	}

	var mode AuthMode
	if err := json.Unmarshal(body, &mode); err != nil {
		return nil, fmt.Errorf("parsing /api/config response: %w", err)
	}

	if !authmode.Valid(mode.Mode) {
		return nil, fmt.Errorf("unsupported auth mode %q from server (client may need updating)", mode.Mode)
	}
	if mode.Mode == authmode.ModeOIDC && strings.TrimSpace(mode.AuthURL) == "" {
		mode.AuthURL = c.serverURL
	}

	return &mode, nil
}

// ensureToken loads a cached token or initiates authentication.
// Caller must hold c.mu.
func (c *Client) ensureToken() error {
	// Check in-memory token first.
	if c.token != "" && time.Now().Add(expiryBuffer).Before(c.tokenExpiry) {
		return nil
	}

	// Try loading from disk cache.
	cached, cachedExpiry, err := loadCachedToken(c.serverURL)
	if err != nil {
		return err
	}
	if cached != "" {
		c.token = cached
		c.tokenExpiry = cachedExpiry
		return nil
	}

	return c.authenticate()
}

// authenticate runs the device flow and caches the resulting token.
// Caller must hold c.mu.
func (c *Client) authenticate() error {
	token, expiresAt, err := RunDeviceFlow(DeviceFlowConfig{
		AuthURL: c.authMode.AuthURL,
	})
	if err != nil {
		return err
	}

	c.token = token
	c.tokenExpiry = expiresAt

	if err := saveCachedToken(c.serverURL, token, expiresAt); err != nil {
		// Non-fatal — token still works for this session.
		_, _ = fmt.Fprintf(stderr, "warning: could not cache auth token: %v\n", err)
	}

	return nil
}
