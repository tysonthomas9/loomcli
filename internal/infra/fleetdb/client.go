// Package fleetdb implements store.Store as an HTTP client against the
// fleet-db service's REST API.
//
// This is the only runtime wiring used by loom serve + loom CLI commands.
// Unit tests may use the test-only internal/infra/memstore package as a store
// double. Local mode still uses this client, pointed at an embedded fleet-db
// subprocess; cloud mode points it at a remote fleet-db service.
//
// Authentication: the client sends X-API-Key plus X-Fleet-API-Key
// (when APIKey is configured) and X-Actor (always — defaults to the
// loom agent name or the OS user). Fleet-db's --auth-dev-mode treats X-Actor as the
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

	// APIKey is sent as X-API-Key and X-Fleet-API-Key. Optional in dev mode.
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

	workspaces *workspaceStore
	repos      *repoStore
	agents     *agentStore
	nodes      *nodeStore
	sessions   *agentSessionStore
	terminals  *terminalSessionStore
	artifacts  *artifactStore
	leases     *agentLeaseStore
	ownership  *agentOwnershipLeaseStore
	commands   *agentCommandStore
	inbox      *agentInboxMessageStore
	drivers    *driverStore
	versions   *driverVersionStore
	profiles   *workerProfileStore
	services   *agentServiceStore
	bindings   *triggerBindingStore
	events     *triggerEventStore
	deliveries *triggerDeliveryStore
	routes     *triggerRouteStore
	runs       *driverRunStore
	steps      *driverStepStore
	taskRuns   *taskRunStore
	workers    *workerStore
	roles      *roleStore
	daemon     *daemonStore
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
	c.inbox = &agentInboxMessageStore{client: c}
	c.drivers = &driverStore{client: c}
	c.versions = &driverVersionStore{client: c}
	c.profiles = &workerProfileStore{client: c}
	c.services = &agentServiceStore{client: c}
	c.bindings = &triggerBindingStore{client: c}
	c.events = &triggerEventStore{client: c}
	c.deliveries = &triggerDeliveryStore{client: c}
	c.routes = &triggerRouteStore{client: c}
	c.runs = &driverRunStore{client: c}
	c.steps = &driverStepStore{client: c}
	c.taskRuns = &taskRunStore{client: c}
	c.workers = &workerStore{client: c}
	c.roles = &roleStore{client: c}
	c.daemon = &daemonStore{client: c}
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

func (c *Client) AgentInboxMessages() store.AgentInboxMessageStore { return c.inbox }

// Drivers returns the DriverStore.
func (c *Client) Drivers() store.DriverStore { return c.drivers }

// DriverVersions returns the DriverVersionStore.
func (c *Client) DriverVersions() store.DriverVersionStore { return c.versions }

func (c *Client) WorkerProfiles() store.WorkerProfileStore { return c.profiles }

func (c *Client) AgentServices() store.AgentServiceStore { return c.services }

func (c *Client) TriggerBindings() store.TriggerBindingStore { return c.bindings }

// TriggerEvents returns the TriggerEventStore.
func (c *Client) TriggerEvents() store.TriggerEventStore { return c.events }

// TriggerDeliveries returns the TriggerDeliveryStore.
func (c *Client) TriggerDeliveries() store.TriggerDeliveryStore { return c.deliveries }

// TriggerRoutes returns the TriggerRouteDispatcher.
func (c *Client) TriggerRoutes() store.TriggerRouteDispatcher { return c.routes }

// DriverRuns returns the DriverRunStore.
func (c *Client) DriverRuns() store.DriverRunStore { return c.runs }

func (c *Client) DriverSteps() store.DriverStepStore { return c.steps }

// TaskRuns returns the TaskRunStore.
func (c *Client) TaskRuns() store.TaskRunStore { return c.taskRuns }

// TaskRunEvents is a compile stub; the fleet-db-backed implementation
// lands in a follow-up chunk.
func (c *Client) TaskRunEvents() store.TaskRunEventStore { return nil }

// Outbox is a compile stub; the fleet-db-backed implementation lands in
// a follow-up chunk.
func (c *Client) Outbox() store.OutboxStore { return nil }

// Workers returns the WorkerStore.
func (c *Client) Workers() store.WorkerStore { return c.workers }

// Roles returns the RoleStore.
func (c *Client) Roles() store.RoleStore { return c.roles }

// Daemon returns the DaemonProfileStore.
func (c *Client) Daemon() store.DaemonProfileStore { return c.daemon }

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
//   - 409 already_exists → domain.ErrAlreadyExists
//   - 409 already_claimed → domain.ErrAlreadyClaimed
//   - 409 invalid_transition → domain.ErrInvalidTransition
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

	return c.doRequest(req, method, path, out)
}

func (c *Client) doRaw(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	c.mu.RLock()
	auth := fleethttp.Auth{BearerToken: c.authToken, APIKey: c.apiKey, Actor: c.actor}
	c.mu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("fleetdb: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if contentType = strings.TrimSpace(contentType); contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	auth.Apply(req)

	return c.doRequest(req, method, path, out)
}

func (c *Client) doRequest(req *http.Request, method, path string, out any) error {
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
	code := extractErrorCode(body)
	prefix := fmt.Sprintf("fleetdb: %s %s: HTTP %d", method, path, status)
	if msg != "" {
		prefix += ": " + msg
	}
	switch status {
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", prefix, domain.ErrNotFound)
	case http.StatusConflict:
		switch code {
		case "already_claimed":
			return fmt.Errorf("%s: %w", prefix, domain.ErrAlreadyClaimed)
		case "invalid_transition":
			return fmt.Errorf("%s: %w", prefix, domain.ErrInvalidTransition)
		case "conflict":
			return fmt.Errorf("%s: %w", prefix, domain.ErrConflict)
		}
		return fmt.Errorf("%s: %w", prefix, domain.ErrAlreadyExists)
	case http.StatusForbidden:
		if strings.Contains(path, "/driver-runs/") {
			return fmt.Errorf("%s: %w", prefix, domain.ErrNotOwner)
		}
		return fmt.Errorf("%s: %w", prefix, domain.ErrConflict)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return fmt.Errorf("%s: %w", prefix, domain.ErrInvalid)
	case http.StatusGone:
		// fleet-db heartbeat: lease exists, token is ours, but it is no
		// longer live (expired or released) — re-acquire is safe.
		return fmt.Errorf("%s: %w", prefix, domain.ErrGone)
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

func extractErrorCode(body []byte) string {
	var structured struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &structured); err != nil {
		return ""
	}
	return structured.Error.Code
}

// pathEscape wraps url.PathEscape so call sites stay compact.
func pathEscape(s string) string { return url.PathEscape(s) }

// withQuery appends the encoded query to path, or returns path unchanged
// when q is empty. Shared by every list endpoint.
func withQuery(path string, q url.Values) string {
	if encoded := q.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
}
