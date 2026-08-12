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
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/platform/fleethttp"
	"github.com/tysonthomas9/loomcli/internal/store"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	// maxResponseBody caps generic JSON response reads at 16 MiB to prevent OOM
	// if fleet-db ever sends a malformed/huge metadata body. Workspace metadata
	// is kilobytes; this is generous.
	maxResponseBody = 16 << 20

	// maxArtifactContentBody is the raw artifact-content contract shared with
	// FleetDB and the task-run artifact upload surface. Artifact payloads include
	// transcripts and patches that may legitimately exceed the generic JSON cap.
	maxArtifactContentBody = 64 << 20
)

// ErrRateLimited identifies a retryable FleetDB admission failure. It must
// never be collapsed into a domain conflict: doing so makes Execution treat a
// transient 429 as a deterministic ownership/generation rejection and can
// strand an otherwise valid Work Item claim.
var ErrRateLimited = errors.New("fleetdb: rate limited")

const (
	FleetDelegatedActorHeader      = "X-Fleet-Delegated-Actor"
	AgentOwnershipLeaseTokenHeader = "X-Agent-Ownership-Lease-Token" //nolint:gosec // HTTP header name, not credential material.
)

var (
	errFleetInvalidDelegatedActor = errors.New("fleetdb: invalid delegated actor")
	errFleetRevisionConflict      = errors.New("fleetdb: workflow catalog revision conflict")
)

// fleetRequester is the shared authenticated JSON execution seam for the
// capability-specific FleetDB transports housed in this adapter package.
type fleetRequester interface {
	Do(context.Context, string, string, any, any) error
	DoWithHeaders(context.Context, string, string, any, any, map[string]string) error
}

// delegatedActorHeaders validates an audit identity before placing it in the
// FleetDB-only delegated-actor header. It must never be serialized in a
// command body.
func delegatedActorHeaders(actor string) (map[string]string, error) {
	trimmed := strings.TrimSpace(actor)
	if trimmed == "" || actor != trimmed || len(actor) > 256 {
		return nil, errFleetInvalidDelegatedActor
	}
	for _, character := range actor {
		if character < 0x20 || character == 0x7f {
			return nil, errFleetInvalidDelegatedActor
		}
	}
	return map[string]string{FleetDelegatedActorHeader: actor}, nil
}

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

	workspaces           *workspaceStore
	repos                *repoStore
	nodes                *nodeStore
	sessions             *agentSessionStore
	terminals            *terminalSessionStore
	artifacts            *artifactStore
	artifactCommands     *artifactCommandStore
	leases               *agentLeaseStore
	ownership            *agentOwnershipLeaseStore
	inbox                *agentInboxMessageStore
	drivers              *driverStore
	versions             *driverVersionStore
	catalog              *workflowCatalogStore
	provisioning         AgentProvisioningTransport
	agentManagement      AgentManagementTransport
	interaction          InteractionTransport
	repositoryAdmissions RepositoryAdmissionTransport
	automation           *automationStore
	profiles             *workerProfileStore
	services             *agentServiceStore
	bindings             *triggerBindingStore
	events               *triggerEventStore
	deliveries           *triggerDeliveryStore
	runs                 *driverRunStore
	steps                *driverStepStore
	taskRuns             *taskRunStore
	execution            *executionStore
	taskEvents           *taskRunEventStore
	outbox               *outboxStore
	awaits               *awaitStore
	workers              *workerStore
	roles                *roleStore

	connectors      *connectorStore
	connectorGrants *connectorGrantStore
	connectorCalls  *connectorAuditStore
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
	c.initializeStores()
	return c, nil
}

func (c *Client) initializeStores() {
	capabilityRequests := &capabilityRequester{client: c}
	c.workspaces = &workspaceStore{client: c}
	c.repos = &repoStore{client: c}
	c.nodes = &nodeStore{client: c}
	c.sessions = &agentSessionStore{client: c}
	c.terminals = &terminalSessionStore{client: c}
	c.artifacts = &artifactStore{client: c}
	c.artifactCommands = &artifactCommandStore{client: c}
	c.leases = &agentLeaseStore{client: c}
	c.inbox = &agentInboxMessageStore{client: c}
	c.drivers = &driverStore{client: c}
	c.versions = &driverVersionStore{client: c}
	c.catalog = &workflowCatalogStore{client: c}
	c.provisioning = newAgentProvisioningTransport(capabilityRequests)
	c.agentManagement = newAgentManagementTransport(capabilityRequests)
	c.ownership = &agentOwnershipLeaseStore{client: c, management: c.agentManagement}
	c.interaction = newInteractionTransport(capabilityRequests)
	c.repositoryAdmissions = newRepositoryAdmissionTransport(capabilityRequests)
	c.automation = &automationStore{client: c}
	c.profiles = &workerProfileStore{client: c}
	c.services = &agentServiceStore{client: c}
	c.bindings = &triggerBindingStore{client: c}
	c.events = &triggerEventStore{client: c}
	c.deliveries = &triggerDeliveryStore{client: c}
	c.runs = &driverRunStore{client: c}
	c.steps = &driverStepStore{client: c}
	c.taskRuns = &taskRunStore{client: c}
	c.execution = &executionStore{client: c}
	c.taskEvents = &taskRunEventStore{client: c}
	c.outbox = &outboxStore{client: c}
	c.awaits = &awaitStore{client: c}
	c.workers = &workerStore{client: c}
	c.roles = &roleStore{client: c}
	c.connectors = &connectorStore{client: c}
	c.connectorGrants = &connectorGrantStore{client: c}
	c.connectorCalls = &connectorAuditStore{client: c}
}

// capabilityRequester is the private bridge from owner-scoped transports to
// the process-wide FleetDB client. It deliberately exposes only authenticated
// JSON execution plus the Role and AgentSession dependencies those transports
// require.
type capabilityRequester struct {
	client *Client
}

func (requester *capabilityRequester) Do(
	ctx context.Context,
	method,
	path string,
	body,
	out any,
) error {
	return requester.client.do(ctx, method, path, body, out)
}

func (requester *capabilityRequester) DoWithHeaders(
	ctx context.Context,
	method,
	path string,
	body,
	out any,
	headers map[string]string,
) error {
	return requester.client.doWithHeaders(ctx, method, path, body, out, headers)
}

func (requester *capabilityRequester) GetRole(
	ctx context.Context,
	workspace,
	name string,
) (*domain.Role, error) {
	return requester.client.roles.Get(ctx, workspace, name)
}

func (requester *capabilityRequester) ListRoles(
	ctx context.Context,
	workspace string,
) ([]*domain.Role, error) {
	return requester.client.roles.List(ctx, workspace)
}

func (requester *capabilityRequester) CreateRole(
	ctx context.Context,
	input store.RoleCreate,
) (*domain.Role, error) {
	return requester.client.roles.Create(ctx, input)
}

func (requester *capabilityRequester) GetAgentSession(
	ctx context.Context,
	workspace,
	sessionID string,
) (*domain.AgentSession, error) {
	return requester.client.sessions.Get(ctx, workspace, sessionID)
}

// Compile-time check.
var _ store.Store = (*Client)(nil)

// Workspaces returns the WorkspaceStore.
func (c *Client) Workspaces() store.WorkspaceStore { return c.workspaces }

// Repos returns the RepoStore.
func (c *Client) Repos() store.RepoStore { return c.repos }

// Nodes returns the NodeStore.
func (c *Client) Nodes() store.NodeStore { return c.nodes }

// AgentSessions returns the AgentSessionStore.
func (c *Client) AgentSessions() store.AgentSessionStore { return c.sessions }

// TerminalSessions returns the TerminalSessionStore.
func (c *Client) TerminalSessions() store.TerminalSessionStore { return c.terminals }

// ArtifactQueries exposes the Artifacts-owned read port without routing UI
// consumers through a legacy catalog adapter.
func (c *Client) ArtifactQueries() artifacts.QueryStore { return c.artifacts }

// ArtifactCommands exposes the narrow owner-fenced Artifacts transport. It
// shares this Client's credentials, tracing, retry policy, and connection pool
// while keeping revision CAS details inside the transport.
func (c *Client) ArtifactCommands() ArtifactTransport { return c.artifactCommands }

// SessionArtifacts exposes the narrow session-owned Artifact content
// transport. It shares this Client's authentication, tracing, retry policy,
// and connection pool and does not expose the legacy composite Store.
func (c *Client) SessionArtifacts() SessionArtifactTransport {
	if c == nil {
		return nil
	}
	return &sessionArtifactStore{client: c}
}

// AgentLeases returns the AgentLeaseStore.
func (c *Client) AgentLeases() store.AgentLeaseStore { return c.leases }

func (c *Client) AgentOwnershipLeases() store.AgentOwnershipLeaseStore { return c.ownership }

func (c *Client) AgentInboxMessages() store.AgentInboxMessageStore { return c.inbox }

// Drivers returns the DriverStore.
func (c *Client) Drivers() store.DriverStore { return c.drivers }

// DriverVersions returns the DriverVersionStore.
func (c *Client) DriverVersions() store.DriverVersionStore { return c.versions }

// WorkflowCatalog exposes the narrow transport surface used by the Workflow
// Catalog adapter. It reuses this Client's authentication, tracing, retry, and
// connection pool; composition must not construct a second FleetDB client for
// the capability.
func (c *Client) WorkflowCatalog() WorkflowCatalogTransport { return c.catalog }

// AgentProvisioning exposes only the durable process-manager progress
// transport. The application workflow receives this through its own adapter,
// never the composite Store or the low-level Client.
func (c *Client) AgentProvisioning() AgentProvisioningTransport { return c.provisioning }

// AgentManagement exposes the narrow Phase 5 Agent identity, Role, desired
// state, and ownership-generation transport. It reuses this Client's trusted
// service credential and keeps delegated audit identity out of request bodies.
func (c *Client) AgentManagement() AgentManagementTransport { return c.agentManagement }

// Interaction exposes the complete Phase 5 authority-validation and atomic
// command transport. It intentionally excludes every legacy non-atomic
// session, terminal, lease, and inbox mutation route.
func (c *Client) Interaction() InteractionTransport { return c.interaction }

// RepositoryAdmissions exposes the service-authenticated process-manager
// transport over the shared FleetDB client. There is no capability-local
// credential or independently configured HTTP path.
func (c *Client) RepositoryAdmissions() RepositoryAdmissionTransport {
	if c == nil {
		return nil
	}
	return c.repositoryAdmissions
}

// Automation exposes the narrow low-level transport used by Automation's
// capability-local FleetDB adapter. It reuses this Client's credentials,
// tracing, retry policy, and connection pool.
func (c *Client) Automation() AutomationTransport { return c.automation }

func (c *Client) WorkerProfiles() store.WorkerProfileStore { return c.profiles }

func (c *Client) AgentServices() store.AgentServiceStore { return c.services }

func (c *Client) delegatedActor() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.actor
}

func (c *Client) TriggerBindings() store.TriggerBindingStore { return c.bindings }

// TriggerEvents returns the TriggerEventStore.
func (c *Client) TriggerEvents() store.TriggerEventStore { return c.events }

// TriggerDeliveries returns the TriggerDeliveryStore.
func (c *Client) TriggerDeliveries() store.TriggerDeliveryStore { return c.deliveries }

// DriverRuns returns the DriverRunStore.
func (c *Client) DriverRuns() store.DriverRunStore { return c.runs }

func (c *Client) DriverSteps() store.DriverStepStore { return c.steps }

// TaskRuns returns the TaskRunStore.
func (c *Client) TaskRuns() store.TaskRunStore { return c.taskRuns }

// Execution exposes the production Execution foundation, including the
// system-only terminal DriverStep convergence command. It reuses the
// process-wide FleetDB client rather than constructing a capability-local HTTP
// client.
func (c *Client) Execution() ExecutionFoundationTransport { return c.execution }

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

func bodyPtr[T any](body map[string]any, key string, value *T) {
	if value != nil {
		body[key] = *value
	}
}

func bodyTimeRFC3339NanoPtr(body map[string]any, key string, value *time.Time) {
	if value != nil {
		body[key] = value.Format(time.RFC3339Nano)
	}
}

func (c *Client) doWithHeaders(ctx context.Context, method, path string, body, out any, headers map[string]string) error {
	_, err := c.doWithHeadersStatus(ctx, method, path, body, out, headers)
	return err
}

// doWithHeadersStatus is the status-observing variant used by routes where a
// successful no-content response has domain meaning distinct from a malformed
// empty success body. Most callers should continue to use doWithHeaders.
func (c *Client) doWithHeadersStatus(ctx context.Context, method, path string, body, out any, headers map[string]string) (int, error) {
	c.mu.RLock()
	auth := fleethttp.Auth{BearerToken: c.authToken, APIKey: c.apiKey, Actor: c.actor}
	c.mu.RUnlock()

	req, err := fleethttp.BuildJSONRequest(ctx, method, c.baseURL+path, auth, body)
	if err != nil {
		return 0, fmt.Errorf("fleetdb: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return c.doRequestStatus(req, method, path, out)
}

func (c *Client) doRaw(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	return c.doRawWithHeaders(ctx, method, path, body, contentType, out, nil)
}

func (c *Client) doRawWithHeaders(ctx context.Context, method, path string, body io.Reader, contentType string, out any, headers map[string]string) error {
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
	for key, value := range headers {
		req.Header.Set(key, value)
	}

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
		return nil, fmt.Errorf(
			"fleetdb: %s %s: %w",
			method,
			path,
			errors.Join(domain.ErrUnavailable, err),
		)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		if readErr != nil {
			return nil, fmt.Errorf(
				"fleetdb: %s %s: HTTP %d (read body: %w)",
				method,
				path,
				resp.StatusCode,
				errors.Join(domain.ErrUnavailable, readErr),
			)
		}
		return nil, classifyHTTPError(method, path, resp.StatusCode, respBody)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArtifactContentBody+1))
	if err != nil {
		return nil, fmt.Errorf(
			"fleetdb: read response (%s %s): %w",
			method,
			path,
			errors.Join(domain.ErrUnavailable, err),
		)
	}
	if len(body) > maxArtifactContentBody {
		return nil, fmt.Errorf("fleetdb: %s %s: artifact content exceeds %d bytes", method, path, maxArtifactContentBody)
	}
	return body, nil
}

func (c *Client) doRequest(req *http.Request, method, path string, out any) error {
	_, err := c.doRequestStatus(req, method, path, out)
	return err
}

func (c *Client) doRequestStatus(req *http.Request, method, path string, out any) (int, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf(
			"fleetdb: %s %s: %w",
			method,
			path,
			errors.Join(domain.ErrUnavailable, err),
		)
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
			return resp.StatusCode, fmt.Errorf(
				"fleetdb: %s %s: HTTP %d (read body: %w)",
				method,
				path,
				resp.StatusCode,
				errors.Join(domain.ErrUnavailable, readErr),
			)
		}
		return resp.StatusCode, classifyHTTPError(method, path, resp.StatusCode, respBody)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return resp.StatusCode, nil
		}
		return resp.StatusCode, fmt.Errorf("fleetdb: decode response (%s %s): %w", method, path, err)
	}
	return resp.StatusCode, nil
}

// classifyHTTPError maps an HTTP status + body into the appropriate
// domain sentinel + descriptive wrap.
//
//nolint:cyclop // This is the central exhaustive HTTP-to-domain error classification table.
func classifyHTTPError(method, path string, status int, body []byte) error {
	code := extractErrorCode(body)
	prefix := formatHTTPErrorPrefix(method, path, status, extractErrorMessage(body))
	if sentinel := automationHTTPErrorSentinel(code); sentinel != nil {
		return fmt.Errorf("%s: %w", prefix, sentinel)
	}
	switch status {
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", prefix, domain.ErrNotFound)
	case http.StatusConflict:
		return classifyConflictHTTPError(prefix, code)
	case http.StatusForbidden:
		return classifyForbiddenHTTPError(prefix, path, code)
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return classifyInvalidHTTPError(prefix, code)
	case http.StatusGone:
		// fleet-db heartbeat: lease exists, token is ours, but it is no
		// longer live (expired or released) — re-acquire is safe.
		return fmt.Errorf("%s: %w", prefix, domain.ErrGone)
	case http.StatusTooManyRequests:
		return fmt.Errorf(
			"%s: %w",
			prefix,
			errors.Join(ErrRateLimited, domain.ErrRateLimited),
		)
	}
	if status >= 500 && strings.Contains(path, "/artifacts/") {
		return fmt.Errorf("%s: %w", prefix, ErrArtifactsUnavailable)
	}
	if status >= 400 && status < 500 {
		return fmt.Errorf("%s: %w", prefix, domain.ErrConflict)
	}
	return fmt.Errorf("%s: %w", prefix, domain.ErrUnavailable)
}

func classifyConflictHTTPError(prefix, code string) error {
	sentinel := domain.ErrAlreadyExists
	switch code {
	case "repository_admission_conflict":
		sentinel = ErrRepositoryAdmissionConflict
	case "repository_admission_fence_lost":
		sentinel = ErrRepositoryAdmissionFenceLost
	case "repository_admission_invalid_transition":
		sentinel = ErrRepositoryAdmissionState
	case "revision_conflict":
		sentinel = ErrWorkflowCatalogRevisionConflict
	case "workflow_catalog_authoring_conflict":
		sentinel = ErrWorkflowCatalogAuthoringConflict
	case "agent_service_revision_conflict":
		sentinel = ErrAgentServiceRevisionConflict
	case "agent_role_revision_conflict":
		sentinel = ErrAgentRoleRevisionConflict
	case "agent_service_desired_state_conflict":
		sentinel = ErrAgentServiceDesiredStateConflict
	case "agent_service_desired_state_idempotency_conflict":
		sentinel = ErrAgentServiceIdempotencyConflict
	case "already_claimed":
		sentinel = domain.ErrAlreadyClaimed
	case "invalid_transition":
		sentinel = domain.ErrInvalidTransition
	case "conflict":
		sentinel = domain.ErrConflict
	case "driver_run_already_resumed":
		// Pending->suspend window: the await resolved before the suspend
		// landed — the run must continue inline, never suspend.
		sentinel = domain.ErrDriverRunAlreadyResumed
	}
	return fmt.Errorf("%s: %w", prefix, sentinel)
}

func classifyForbiddenHTTPError(prefix, path, code string) error {
	if code == "await_actor_forbidden" {
		return fmt.Errorf("%s: %w", prefix, domain.ErrAwaitActorForbidden)
	}
	if strings.Contains(path, "/driver-runs/") || strings.Contains(path, "/task-runs/") ||
		strings.Contains(path, "/agent-ownership-leases/") ||
		strings.Contains(path, "/agent-session-authority/") ||
		strings.Contains(path, "/desired-state/owned") ||
		strings.Contains(path, "/artifact-commands/") || strings.Contains(path, "/artifacts/") {
		return fmt.Errorf("%s: %w", prefix, domain.ErrNotOwner)
	}
	return fmt.Errorf("%s: %w", prefix, domain.ErrConflict)
}

func classifyInvalidHTTPError(prefix, code string) error {
	var sentinel error
	switch code {
	case "workflow_catalog_version_ownership":
		sentinel = ErrWorkflowCatalogVersionOwnership
	case "workflow_catalog_version_not_validated":
		sentinel = ErrWorkflowCatalogVersionNotValidated
	case "workflow_catalog_version_not_approved":
		sentinel = ErrWorkflowCatalogVersionNotApproved
	default:
		// Structured await validation codes map back onto their domain
		// sentinels (each wraps domain.ErrInvalid).
		sentinel = awaitErrSentinel(code)
	}
	if sentinel == nil {
		sentinel = domain.ErrInvalid
	}
	return fmt.Errorf("%s: %w", prefix, sentinel)
}

var automationHTTPErrorSentinels = map[string]error{
	"automation_idempotency_required":              ErrAutomationInvalid,
	"automation_invalid_admission":                 ErrAutomationInvalid,
	"automation_route_not_found":                   ErrAutomationRouteNotFound,
	"automation_parent_run_not_found":              ErrAutomationParentRunNotFound,
	"automation_execution_owner_conflict":          ErrAutomationExecutionOwnerConflict,
	"automation_idempotency_conflict":              ErrAutomationIdempotencyConflict,
	"automation_binding_snapshot_conflict":         ErrAutomationBindingSnapshotConflict,
	"automation_catalog_snapshot_conflict":         ErrAutomationCatalogSnapshotConflict,
	"automation_hop_depth_exceeded":                ErrAutomationHopDepthExceeded,
	"automation_catalog_version_unavailable":       ErrAutomationCatalogUnavailable,
	"automation_fanout_limit_exceeded":             ErrAutomationFanoutLimitExceeded,
	"automation_admission_unavailable":             ErrAutomationAdmissionUnavailable,
	"automation_delivery_not_found":                ErrAutomationDeliveryNotFound,
	"automation_delivery_not_dispatchable":         ErrAutomationDeliveryNotDispatchable,
	"automation_delivery_transition_conflict":      ErrAutomationDeliveryTransitionConflict,
	"automation_payload_digest_mismatch":           ErrAutomationPayloadDigestMismatch,
	"automation_binding_not_found":                 ErrAutomationBindingNotFound,
	"automation_binding_dispatch_replay_not_found": ErrAutomationBindingDispatchReplayNotFound,
	"automation_admission_replay_not_found":        ErrAutomationAdmissionReplayNotFound,
	"automation_cron_occurrence_not_found":         ErrAutomationCronOccurrenceNotFound,
	"automation_cron_completion_conflict":          ErrAutomationCronCompletionConflict,
	"automation_managed_binding_conflict":          ErrAutomationManagedBindingConflict,
}

func automationHTTPErrorSentinel(code string) error { return automationHTTPErrorSentinels[code] }

func formatHTTPErrorPrefix(method, path string, status int, message string) string {
	prefix := fmt.Sprintf("fleetdb: %s %s: HTTP %d", method, path, status)
	if message != "" {
		prefix += ": " + message
	}
	return prefix
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
