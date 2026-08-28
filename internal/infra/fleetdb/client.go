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
	APIKey string //nolint:gosec // G117: fleet-db API key intentionally carried by client config.

	// Actor is sent as X-Actor on every request. Identifies the caller
	// for audit + (in dev-mode) authorization.
	Actor string

	// AuthToken is a JWT bearer token for production auth. When set,
	// sent as `Authorization: Bearer <token>`. Mutate post-construction
	// via SetAuthToken — safe for concurrent use.
	AuthToken string //nolint:gosec // G117: bearer token intentionally carried by client config.

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
	taskEvents *taskRunEventStore
	outbox     *outboxStore
	awaits     *awaitStore
	workers    *workerStore
	roles      *roleStore
	skills     *skillStore
	matLeases  *skillMaterializationLeaseStore
	skillPacks *skillPackStore
	daemon     *daemonStore

	connectors      *connectorStore
	connectorGrants *connectorGrantStore
	connectorCalls  *connectorAuditStore
}

// New constructs a fleet-db client. Returns an error if BaseURL is empty.
//
//nolint:funlen // One sub-store wire per line; splitting hides what the client serves.
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
	c.taskEvents = &taskRunEventStore{client: c}
	c.outbox = &outboxStore{client: c}
	c.awaits = &awaitStore{client: c}
	c.workers = &workerStore{client: c}
	c.roles = &roleStore{client: c}
	c.skills = &skillStore{client: c}
	c.matLeases = &skillMaterializationLeaseStore{client: c}
	c.skillPacks = &skillPackStore{client: c}
	c.daemon = &daemonStore{client: c}
	c.connectors = &connectorStore{client: c}
	c.connectorGrants = &connectorGrantStore{client: c}
	c.connectorCalls = &connectorAuditStore{client: c}
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

// TaskRunEvents returns the TaskRunEventStore.
func (c *Client) TaskRunEvents() store.TaskRunEventStore { return c.taskEvents }

// Outbox returns the OutboxStore.
func (c *Client) Outbox() store.OutboxStore { return c.outbox }

// Awaits returns the AwaitStore (fleet-db await routes, chunk AW5).
func (c *Client) Awaits() store.AwaitStore { return c.awaits }

// Workers returns the WorkerStore.
func (c *Client) Workers() store.WorkerStore { return c.workers }

// Roles returns the RoleStore.
func (c *Client) Roles() store.RoleStore { return c.roles }

// Skills returns the SkillStore.
func (c *Client) Skills() store.SkillStore { return c.skills }

// SkillMaterializationLeases returns the ephemeral materialization lease store.
func (c *Client) SkillMaterializationLeases() store.SkillMaterializationLeaseStore {
	return c.matLeases
}

// SkillPacks returns the SkillPackStore.
func (c *Client) SkillPacks() store.SkillPackStore { return c.skillPacks }

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
//   - 409 skill_materialization_lease_conflict → typed holder metadata
//   - 400/422 → domain.ErrInvalid
//   - 4xx other → domain.ErrConflict (best fit; callers can inspect msg)
//   - 503 skill_materialization_lease_store_unavailable → dedicated sentinel
//   - 5xx → fmt.Errorf wrapping the body
//
// 204 No Content is treated as success with no body.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	return c.doWithHeaders(ctx, method, path, body, out, nil)
}

func (c *Client) doWithHeaders(ctx context.Context, method, path string, body, out any, headers map[string]string) error {
	_, _, err := c.doWithResponse(ctx, method, path, body, out, headers)
	return err
}

// doWithResponse is doWithHeaders plus the parts of the response that are not
// the body: the status code and the response headers.
//
// Both matter to conditional writes. The status separates a 201 create from a
// 200 update on an upsert, which is what an import or a sync reports back, and
// the headers carry the ETag a per-document read hands to the next write.
func (c *Client) doWithResponse(ctx context.Context, method, path string, body, out any, headers map[string]string) (int, http.Header, error) {
	return c.doWithResponseRedirectPolicy(ctx, method, path, body, out, headers, true)
}

// doWithResponseNoRedirect is the mutation transport for skills. A redirect
// on a skill write is never legitimate: following a 307/308 replays the body
// and can change the authorization lane selected by the original path.
func (c *Client) doWithResponseNoRedirect(ctx context.Context, method, path string, body, out any, headers map[string]string) (int, http.Header, error) {
	return c.doWithResponseRedirectPolicy(ctx, method, path, body, out, headers, false)
}

func (c *Client) doWithResponseRedirectPolicy(ctx context.Context, method, path string, body, out any, headers map[string]string, followRedirects bool) (int, http.Header, error) {
	c.mu.RLock()
	auth := fleethttp.Auth{BearerToken: c.authToken, APIKey: c.apiKey, Actor: c.actor}
	c.mu.RUnlock()

	req, err := fleethttp.BuildJSONRequest(ctx, method, c.baseURL+path, auth, body)
	if err != nil {
		return 0, nil, fmt.Errorf("fleetdb: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	httpClient := c.http
	if !followRedirects {
		clone := *c.http
		clone.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		httpClient = &clone
	}
	return c.doRequestResponseWithClient(httpClient, req, method, path, out)
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

func (c *Client) doBytes(ctx context.Context, method, path string) ([]byte, error) {
	c.mu.RLock()
	auth := fleethttp.Auth{BearerToken: c.authToken, APIKey: c.apiKey, Actor: c.actor}
	c.mu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("fleetdb: build request: %w", err)
	}
	req.Header.Set("Accept", "*/*")
	auth.Apply(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fleetdb: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		if readErr != nil {
			return nil, fmt.Errorf("fleetdb: %s %s: HTTP %d (read body: %w)", method, path, resp.StatusCode, readErr)
		}
		return nil, classifyHTTPError(method, path, resp.StatusCode, respBody)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return nil, fmt.Errorf("fleetdb: read response (%s %s): %w", method, path, err)
	}
	if len(body) > maxResponseBody {
		return nil, fmt.Errorf("fleetdb: %s %s: response body exceeds %d bytes", method, path, maxResponseBody)
	}
	return body, nil
}

func (c *Client) doRequest(req *http.Request, method, path string, out any) error {
	_, _, err := c.doRequestResponse(req, method, path, out)
	return err
}

func (c *Client) doRequestResponse(req *http.Request, method, path string, out any) (int, http.Header, error) {
	return c.doRequestResponseWithClient(c.http, req, method, path, out)
}

func (c *Client) doRequestResponseWithClient(httpClient *http.Client, req *http.Request, method, path string, out any) (int, http.Header, error) {
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("fleetdb: %s %s: %w", method, path, err)
	}
	defer func() {
		// Drain so the underlying connection can be returned to the
		// keep-alive pool even when callers don't consume the full body.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return resp.StatusCode, resp.Header, fmt.Errorf("fleetdb: %s %s: unexpected HTTP %d redirect", method, path, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		if readErr != nil {
			return resp.StatusCode, resp.Header, fmt.Errorf("fleetdb: %s %s: HTTP %d (read body: %w)", method, path, resp.StatusCode, readErr)
		}
		return resp.StatusCode, resp.Header, classifyHTTPError(method, path, resp.StatusCode, respBody)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, resp.Header, nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return resp.StatusCode, resp.Header, nil
		}
		return resp.StatusCode, resp.Header, fmt.Errorf("fleetdb: decode response (%s %s): %w", method, path, err)
	}
	return resp.StatusCode, resp.Header, nil
}

// classifyHTTPError maps an HTTP status + body into the appropriate
// domain sentinel + descriptive wrap.
//
//nolint:cyclop,funlen // One status/code classification table; each arm is one sentinel.
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
		case skillMaterializationLeaseConflictCode:
			return skillMaterializationLeaseConflictError(prefix, body)
		case skillMaterializationLeaseTokenMismatchCode:
			return fmt.Errorf("%s: %w", prefix, domain.ErrSkillMaterializationLeaseTokenMismatch)
		case "already_claimed":
			return fmt.Errorf("%s: %w", prefix, domain.ErrAlreadyClaimed)
		case "invalid_transition":
			return fmt.Errorf("%s: %w", prefix, domain.ErrInvalidTransition)
		case "conflict":
			return fmt.Errorf("%s: %w", prefix, domain.ErrConflict)
		case skillProvenanceConflictCode:
			// Ownership refusal: kept apart from every other 409 because it is
			// the one a caller cannot fix by retrying — see skill.go.
			return skillProvenanceConflictError(prefix, body)
		case "driver_run_already_resumed":
			// Pending->suspend window: the await resolved before the suspend
			// landed — the run must continue inline, never suspend.
			return fmt.Errorf("%s: %w", prefix, domain.ErrDriverRunAlreadyResumed)
		}
		return fmt.Errorf("%s: %w", prefix, domain.ErrAlreadyExists)
	case http.StatusForbidden:
		if code == "await_actor_forbidden" {
			return fmt.Errorf("%s: %w", prefix, domain.ErrAwaitActorForbidden)
		}
		if strings.Contains(path, "/driver-runs/") {
			return fmt.Errorf("%s: %w", prefix, domain.ErrNotOwner)
		}
		if isSkillAPIPath(path) {
			return fmt.Errorf("%s: %w", prefix, domain.ErrSkillForbidden)
		}
		return fmt.Errorf("%s: %w", prefix, domain.ErrConflict)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		// Structured await validation codes map back onto their domain
		// sentinels (each wraps domain.ErrInvalid).
		if sentinel := awaitErrSentinel(code); sentinel != nil {
			return fmt.Errorf("%s: %w", prefix, sentinel)
		}
		return fmt.Errorf("%s: %w", prefix, domain.ErrInvalid)
	case http.StatusGone:
		// fleet-db heartbeat: lease exists, token is ours, but it is no
		// longer live (expired or released) — re-acquire is safe.
		return fmt.Errorf("%s: %w", prefix, domain.ErrGone)
	case http.StatusPreconditionFailed:
		// A failed If-Match on a conditional write. Distinct from every 409
		// above because this one a caller fixes by re-reading and merging.
		if code == preconditionFailedCode {
			return skillPreconditionError(prefix, body)
		}
	}
	if status == http.StatusServiceUnavailable && code == skillMaterializationLeaseStoreUnavailableCode {
		return fmt.Errorf("%s: %w", prefix, domain.ErrSkillMaterializationLeaseStoreUnavailable)
	}
	if status >= 400 && status < 500 {
		return fmt.Errorf("%s: %w", prefix, domain.ErrConflict)
	}
	return errors.New(prefix)
}

// isSkillAPIPath identifies only the skill and skill-materialization lease
// route families. A repository or another resource may legitimately be named
// "skills"; its 403 must keep the generic classification rather than
// acquiring skill-specific semantics.
func isSkillAPIPath(requestPath string) bool {
	requestPath, _, _ = strings.Cut(requestPath, "?")
	segments := strings.Split(strings.Trim(requestPath, "/"), "/")
	if len(segments) < 4 || segments[0] != "api" || segments[1] != "v1" {
		return false
	}
	if segments[3] == "skills" || segments[3] == "skill-materialization-leases" {
		return true
	}
	return len(segments) >= 6 && segments[3] == "roles" && segments[5] == "skills"
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

// extractErrorMeta pulls fleet-db's structured error meta out of the envelope.
// The meta is where a machine-readable error carries the facts a caller has to
// act on — which revision it held versus which one is stored, who owns the
// record it was refused — that reconstructing from the message would mean
// parsing prose.
func extractErrorMeta(body []byte) map[string]string {
	var structured struct {
		Error struct {
			Meta map[string]string `json:"meta"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &structured); err != nil {
		return nil
	}
	return structured.Error.Meta
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
