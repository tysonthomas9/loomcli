// Package fleetdb implements store.Store as an HTTP client against the
// fleet-db service's REST API.
//
// This is the only runtime wiring used by loom serve + loom CLI commands.
// Unit tests may use the test-only internal/infra/memstore package as a store
// double. Local mode still uses this client, pointed at an embedded fleet-db
// subprocess; cloud mode points it at a remote fleet-db service.
//
// Authentication: the client sends X-Fleet-API-Key (when APIKey is
// configured) and X-Actor (always — defaults to the loom agent name or
// the OS user). Fleet-db's --auth-dev-mode treats X-Actor as the
// authenticated identity; production deployments should configure
// JWT bearer tokens via SetAuthToken.
package fleetdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/fleethttp"
	"github.com/tysonthomas9/loomcli/internal/store"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// maxResponseBody caps response reads at 16 MiB to prevent OOM if
// fleet-db ever sends a malformed/huge body. Workspace metadata is
// kilobytes; this is generous.
const maxResponseBody = 16 << 20

// Config holds connection parameters for the fleet-db HTTP client.
type Config struct {
	// BaseURL is the fleet-db base URL, e.g. "http://localhost:8080".
	// Required. Trailing slash trimmed.
	BaseURL string

	// APIKey is sent as X-Fleet-API-Key. Optional in dev mode.
	APIKey string

	// Actor is sent as X-Actor on every request. Identifies the caller
	// for audit + (in dev-mode) authorization.
	Actor string

	// AuthToken is a JWT bearer token for production auth. When set,
	// sent as `Authorization: Bearer <token>`. Mutate post-construction
	// via SetAuthToken — safe for concurrent use.
	AuthToken string

	// HTTPClient is an optional override. When nil, a new http.Client
	// with default settings is used. Production callers should inject a
	// transport-pooled client.
	HTTPClient *http.Client
}

// Client is the fleet-db HTTP client. Implements store.Store.
type Client struct {
	baseURL string
	http    *http.Client

	mu        sync.RWMutex
	apiKey    string
	actor     string
	authToken string

	workspaces   *workspaceStore
	repos        *repoStore
	agents       *agentStore
	nodes        *nodeStore
	sessions     *agentSessionStore
	terminals    *terminalSessionStore
	artifacts    *artifactStore
	leases       *agentLeaseStore
	ownership    *agentOwnershipLeaseStore
	commands     *agentCommandStore
	roles        *roleStore
	daemon       *daemonStore
	defVersions  *definitionVersionStore
	workflowDefs *workflowDefinitionStore
	workflowRuns *workflowRunStore
	taskRuns     *taskRunStore
	runEvents    *runEventStore
	runtimes     *runtimeProfileStore
	routes       *routeBindingStore
	triggers     *triggerBindingStore
}

// New constructs a fleet-db client. Returns an error if BaseURL is empty.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("fleetdb: BaseURL required")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}
	}
	c := &Client{
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		http:      httpClient,
		apiKey:    cfg.APIKey,
		actor:     cfg.Actor,
		authToken: cfg.AuthToken,
	}
	c.workspaces = &workspaceStore{client: c}
	c.repos = &repoStore{client: c}
	c.agents = &agentStore{client: c}
	c.nodes = &nodeStore{client: c}
	c.sessions = &agentSessionStore{client: c}
	c.terminals = &terminalSessionStore{client: c}
	c.artifacts = &artifactStore{client: c}
	c.leases = &agentLeaseStore{client: c}
	c.ownership = &agentOwnershipLeaseStore{client: c}
	c.commands = &agentCommandStore{client: c}
	c.roles = &roleStore{client: c}
	c.daemon = &daemonStore{client: c}
	c.defVersions = &definitionVersionStore{client: c}
	c.workflowDefs = &workflowDefinitionStore{client: c}
	c.workflowRuns = &workflowRunStore{client: c}
	c.taskRuns = &taskRunStore{client: c}
	c.runEvents = &runEventStore{client: c}
	c.runtimes = &runtimeProfileStore{client: c}
	c.routes = &routeBindingStore{client: c}
	c.triggers = &triggerBindingStore{client: c}
	return c, nil
}

// Compile-time check.
var _ store.Store = (*Client)(nil)

// Workspaces returns the WorkspaceStore.
func (c *Client) Workspaces() store.WorkspaceStore { return c.workspaces }

// Repos returns the RepoStore.
func (c *Client) Repos() store.RepoStore { return c.repos }

// Agents returns the AgentStore.
func (c *Client) Agents() store.AgentStore { return c.agents }

// Nodes returns the NodeStore.
func (c *Client) Nodes() store.NodeStore { return c.nodes }

// AgentSessions returns the AgentSessionStore.
func (c *Client) AgentSessions() store.AgentSessionStore { return c.sessions }

// TerminalSessions returns the TerminalSessionStore.
func (c *Client) TerminalSessions() store.TerminalSessionStore { return c.terminals }

// Artifacts returns the ArtifactStore.
func (c *Client) Artifacts() store.ArtifactStore { return c.artifacts }

// AgentLeases returns the AgentLeaseStore.
func (c *Client) AgentLeases() store.AgentLeaseStore { return c.leases }

func (c *Client) AgentOwnershipLeases() store.AgentOwnershipLeaseStore { return c.ownership }

func (c *Client) AgentCommands() store.AgentCommandStore { return c.commands }

// Roles returns the RoleStore.
func (c *Client) Roles() store.RoleStore { return c.roles }

// Daemon returns the DaemonProfileStore.
func (c *Client) Daemon() store.DaemonProfileStore { return c.daemon }

func (c *Client) DefinitionVersions() store.DefinitionVersionStore { return c.defVersions }

func (c *Client) WorkflowDefinitions() store.WorkflowDefinitionStore { return c.workflowDefs }

func (c *Client) WorkflowRuns() store.WorkflowRunStore { return c.workflowRuns }

func (c *Client) TaskRuns() store.TaskRunStore { return c.taskRuns }

func (c *Client) RunEvents() store.RunEventStore { return c.runEvents }

func (c *Client) RuntimeProfiles() store.RuntimeProfileStore { return c.runtimes }

func (c *Client) RouteBindings() store.RouteBindingStore { return c.routes }

func (c *Client) TriggerBindings() store.TriggerBindingStore { return c.triggers }

// Close is a no-op — HTTP clients hold no resources beyond the
// transport's connection pool, and that is shared / not owned by us.
func (c *Client) Close() error { return nil }

// SetAuthToken updates the bearer token used on subsequent requests.
// Safe for concurrent use.
func (c *Client) SetAuthToken(token string) {
	c.mu.Lock()
	c.authToken = token
	c.mu.Unlock()
}

// SetAPIKey updates the API key used on subsequent requests. Safe for
// concurrent use.
func (c *Client) SetAPIKey(key string) {
	c.mu.Lock()
	c.apiKey = key
	c.mu.Unlock()
}

// do executes an HTTP request, decoding the response into out (when
// non-nil). The body parameter, when non-nil, is JSON-marshaled and
// sent as the request body with Content-Type: application/json.
//
// HTTP error responses are mapped to domain sentinel errors:
//   - 404 → domain.ErrNotFound
//   - 409 → domain.ErrAlreadyExists
//   - 400/422 → domain.ErrInvalid
//   - 4xx other → domain.ErrConflict (best fit; callers can inspect msg)
//   - 5xx → fmt.Errorf wrapping the body
//
// 204 No Content is treated as success with no body.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	return c.doWithHeaders(ctx, method, path, body, out, nil)
}

func (c *Client) doWithHeaders(ctx context.Context, method, path string, body, out any, headers map[string]string) error {
	c.mu.RLock()
	auth := fleethttp.Auth{BearerToken: c.authToken, APIKey: c.apiKey, Actor: c.actor}
	c.mu.RUnlock()

	req, err := fleethttp.BuildJSONRequest(ctx, method, c.baseURL+path, auth, body)
	if err != nil {
		return fmt.Errorf("fleetdb: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("fleetdb: %s %s: %w", method, path, err)
	}
	defer func() {
		// Drain so the underlying connection can be returned to the
		// keep-alive pool even when callers don't consume the full body.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode >= 400 {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		if readErr != nil {
			return fmt.Errorf("fleetdb: %s %s: HTTP %d (read body: %w)", method, path, resp.StatusCode, readErr)
		}
		return classifyHTTPError(method, path, resp.StatusCode, respBody)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("fleetdb: decode response (%s %s): %w", method, path, err)
	}
	return nil
}

// classifyHTTPError maps an HTTP status + body into the appropriate
// domain sentinel + descriptive wrap.
func classifyHTTPError(method, path string, status int, body []byte) error {
	msg := extractErrorMessage(body)
	prefix := fmt.Sprintf("fleetdb: %s %s: HTTP %d", method, path, status)
	if msg != "" {
		prefix += ": " + msg
	}
	switch status {
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", prefix, domain.ErrNotFound)
	case http.StatusConflict:
		return fmt.Errorf("%s: %w", prefix, domain.ErrAlreadyExists)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return fmt.Errorf("%s: %w", prefix, domain.ErrInvalid)
	}
	if status >= 400 && status < 500 {
		return fmt.Errorf("%s: %w", prefix, domain.ErrConflict)
	}
	return errors.New(prefix)
}

// extractErrorMessage delegates to fleethttp.ExtractErrorMessage and
// adds a last-resort trimmed-body fallback for unknown error shapes —
// preserved for debuggability when fleet-db emits a message in neither
// envelope dialect.
func extractErrorMessage(body []byte) string {
	if msg := fleethttp.ExtractErrorMessage(body); msg != "" {
		return msg
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// pathEscape wraps url.PathEscape so call sites stay compact.
func pathEscape(s string) string { return url.PathEscape(s) }
