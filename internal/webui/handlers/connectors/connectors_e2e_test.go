// Package connectors holds the connector + vault end-to-end verification
// suite (CV14): a workspace admin provisions a connector and deny-by-default
// grants through the same store surface the `loom connector` CLI drives, a
// signed GitHub webhook is verified against the connector's inbound secret
// and admits a workflow run, and that run — holding ONLY its run-scoped
// driver identity — performs connector egress through the full production
// pipeline: driver-op HTTP surface → server-side binding resolution → grant
// check → just-in-time vault unseal → real GitHub provider against a stub
// upstream → connector-call journal rows for granted AND refused outcomes.
//
// The load-bearing assertion is invariant §9.5: no secret material (outbound
// credentials before or after rotation, inbound secrets, the vault key) ever
// appears in any HTTP response, any audit row, or the spawned workflow's
// environment. The only place a credential may appear is the Authorization
// header presented to the upstream provider.
package connectors

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	providers "github.com/tysonthomas9/loomcli/internal/infra/connectorsproviders"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorsvault"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/testutil"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/driverapi"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/webhooks"
)

// Secret material under test. The §9.5 scan asserts none of these strings
// ever leave the trust boundary; everything else in the suite is fair game.
const (
	e2eWorkspace = "WS"

	// outboundCredentialV1 is the plaintext GitHub credential sealed into the
	// vault at provisioning time.
	outboundCredentialV1 = "ghp-e2e-outbound-credential-v1-5f4dcc3b"
	// outboundCredentialV2 replaces it during the mid-flight rotation.
	outboundCredentialV2 = "ghp-e2e-outbound-credential-v2-7c222fb2"

	// inboundSecretV1/V2 are the webhook HMAC secrets before/after rotation.
	inboundSecretV1 = "whsec-e2e-inbound-v1"
	inboundSecretV2 = "whsec-e2e-inbound-v2"

	// legacyBindingSecret is configured on the TriggerBinding. Once a
	// connector exists for the source kind it must STOP verifying (the
	// connector inbound secret is the verification root, CV12).
	legacyBindingSecret = "legacy-binding-secret-do-not-accept"
)

// e2eSecrets is every string the §9.5 leak scan hunts for.
var e2eSecrets = []string{
	outboundCredentialV1,
	outboundCredentialV2,
	inboundSecretV1,
	inboundSecretV2,
	legacyBindingSecret,
}

// --- fake GitHub upstream -------------------------------------------------

// upstreamCall is one request observed by the fake GitHub API.
type upstreamCall struct {
	Method         string
	Path           string
	Authorization  string
	IdempotencyKey string
	Body           map[string]any
}

// fakeGitHub stubs the two GitHub REST endpoints the suite exercises: the
// merge endpoint with GitHub's native server-side sha precondition (409 when
// the head moved) and the pull-request read. It records every request so the
// suite can assert exactly where the credential traveled.
type fakeGitHub struct {
	mu      sync.Mutex
	headSha string
	merged  bool
	calls   []upstreamCall
	server  *httptest.Server
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{headSha: "headsha-1"}
	g.server = httptest.NewServer(http.HandlerFunc(g.handle))
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGitHub) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)

	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, upstreamCall{
		Method:         r.Method,
		Path:           r.URL.Path,
		Authorization:  r.Header.Get("Authorization"),
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Body:           body,
	})

	switch {
	case r.Method == http.MethodPut && r.URL.Path == "/repos/octocat/hello/pulls/7/merge":
		sha, _ := body["sha"].(string)
		if sha != g.headSha {
			// GitHub's native sha precondition: head moved -> 409, NO merge.
			writeUpstreamJSON(w, http.StatusConflict, map[string]any{
				"message": "Head branch was modified. Review and try the merge again.",
			})
			return
		}
		g.merged = true
		writeUpstreamJSON(w, http.StatusOK, map[string]any{
			"merged": true,
			"sha":    "merge-commit-for-" + sha,
		})
	case r.Method == http.MethodGet && r.URL.Path == "/repos/octocat/hello/pulls/7":
		writeUpstreamJSON(w, http.StatusOK, map[string]any{
			"number": 7,
			"state":  "open",
			"title":  "Add connector e2e coverage",
			"merged": g.merged,
			"head":   map[string]any{"sha": g.headSha, "ref": "feature/e2e"},
			"base":   map[string]any{"sha": "basesha-1", "ref": "main"},
		})
	default:
		writeUpstreamJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
}

func writeUpstreamJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// setHead simulates a push to the PR branch after the workflow pinned a sha.
func (g *fakeGitHub) setHead(sha string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.headSha = sha
}

func (g *fakeGitHub) snapshot() (calls []upstreamCall, merged bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]upstreamCall(nil), g.calls...), g.merged
}

// --- serve-side harness ---------------------------------------------------

// e2eHarness wires the production modules (webhooks ingress + driver-op API)
// over one memstore behind one httptest server, with a real Dispatcher, a
// real AES-256-GCM vault, and the real GitHub provider pointed at fakeGitHub.
type e2eHarness struct {
	store  store.Store
	vault  *connectorsvault.Vault
	github *fakeGitHub
	server *httptest.Server

	// responses captures every raw HTTP response body the serve surface
	// produced, in order, for the final §9.5 scan.
	mu        sync.Mutex
	responses [][]byte

	runID   string
	nodeID  string
	leaseID string
	fence   int64

	runTokenKey []byte
	runToken    string
}

// connectorE2EAdmission is test-only compatibility wiring for this legacy
// memstore journey. Production webhook transport has no TriggerRoutes
// fallback; it receives the real Automation API from serve composition.
type connectorE2EAdmission struct{ st store.Store }

func (adapter connectorE2EAdmission) AdmitEvent(ctx context.Context, _ automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
	result, err := adapter.st.TriggerRoutes().DispatchTriggerRouteV2(ctx, command.WorkspaceKey, command.RouteKey, store.TriggerRouteDispatch{
		IdempotencyKey: command.SourceKind + ":" + command.SourceEventID,
		SourceEventID:  command.SourceEventID, EventType: command.EventType,
		SubjectRef: command.SubjectRef, ActorRef: command.ActorRef,
		SignatureStatus: "verified", RawPayloadRef: command.RawPayloadRef,
		RawPayloadDigest: command.RawPayloadDigest, Payload: command.Payload,
		SubjectAttrs: command.SubjectAttrs,
	})
	if err != nil {
		return nil, err
	}
	deliveries := make([]*automation.Delivery, 0, len(result.Deliveries))
	for _, delivery := range result.Deliveries {
		deliveries = append(deliveries, &automation.Delivery{
			DeliveryID: delivery.DeliveryID, TriggerBindingID: delivery.BindingID,
			DriverRunID: delivery.RunID, Status: delivery.Status,
			RejectionReason: delivery.RejectionReason,
		})
	}
	return &automation.AdmissionResult{Deliveries: deliveries}, nil
}

type connectorE2EQueries struct{ st store.Store }

func (adapter connectorE2EQueries) GetEvent(ctx context.Context, workspace, eventID string) (*automation.Event, error) {
	return adapter.st.TriggerEvents().Get(ctx, workspace, eventID)
}

func (adapter connectorE2EQueries) ListEvents(ctx context.Context, workspace string, filter automation.EventFilter) ([]*automation.Event, error) {
	return adapter.st.TriggerEvents().List(ctx, workspace, store.TriggerEventFilter{
		SourceKind: filter.SourceKind, TriggerBindingID: filter.BindingID, Limit: filter.Limit,
	})
}

func (adapter connectorE2EQueries) GetDelivery(ctx context.Context, workspace, deliveryID string) (*automation.Delivery, error) {
	return adapter.st.TriggerDeliveries().Get(ctx, workspace, deliveryID)
}

func (adapter connectorE2EQueries) ListDeliveries(ctx context.Context, workspace string, filter automation.DeliveryFilter) ([]*automation.Delivery, error) {
	return adapter.st.TriggerDeliveries().List(ctx, workspace, store.TriggerDeliveryFilter{
		TriggerEventID: filter.EventID, TriggerBindingID: filter.BindingID,
		Status: filter.Status, Limit: filter.Limit,
	})
}

type connectorE2EAuthorityProvider struct{}

func (connectorE2EAuthorityProvider) AuthorityForVerifiedWebhook(context.Context, webhookingestion.AuthorityRequest) (authority.WebhookAuthority, error) {
	return authority.WebhookAuthority{}, nil
}

// connectorE2ETaskRunCommands fills the unrelated atomic TaskRun ports that
// the production Execution composition requires. This connector journey never
// invokes them; returning ErrUnavailable keeps an accidental call fail-closed.
type connectorE2ETaskRunCommands struct{}

func (connectorE2ETaskRunCommands) ReplayTaskRunRequest(context.Context, execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return execution.RequestTaskRunResult{}, execution.ErrUnavailable
}

func (connectorE2ETaskRunCommands) RequestTaskRun(context.Context, execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return execution.RequestTaskRunResult{}, execution.ErrUnavailable
}

func (connectorE2ETaskRunCommands) ClaimTaskRun(context.Context, execution.ClaimTaskRunCommand) (execution.ClaimTaskRunResult, error) {
	return execution.ClaimTaskRunResult{}, execution.ErrUnavailable
}

func (connectorE2ETaskRunCommands) UpdateTaskRunWorkItemDesign(context.Context, execution.UpdateTaskRunWorkItemDesignCommand) (execution.UpdateTaskRunWorkItemDesignResult, error) {
	return execution.UpdateTaskRunWorkItemDesignResult{}, execution.ErrUnavailable
}

func (connectorE2ETaskRunCommands) RequeueTaskRun(context.Context, execution.RequeueTaskRunCommand) (execution.RequeueTaskRunResult, error) {
	return execution.RequeueTaskRunResult{}, execution.ErrUnavailable
}

func (connectorE2ETaskRunCommands) ExhaustTaskRunRetries(context.Context, execution.ExhaustTaskRunRetriesCommand) (execution.ExhaustTaskRunRetriesResult, error) {
	return execution.ExhaustTaskRunRetriesResult{}, execution.ErrUnavailable
}

func newE2EHarness(t *testing.T) *e2eHarness {
	t.Helper()
	h := &e2eHarness{
		store:       memstore.New(),
		github:      newFakeGitHub(t),
		runTokenKey: bytes.Repeat([]byte{0x51}, 32),
	}

	vault, err := connectorsvault.NewVault(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	h.vault = vault

	h.provisionWorkspace(t)

	registry := providers.NewRegistry()
	if err := registry.Register(connectorsmodule.ConnectorSourceGitHub,
		providers.NewGitHub(h.github.server.Client(), h.github.server.URL)); err != nil {
		t.Fatalf("Register github provider: %v", err)
	}
	dispatcher, err := connectorsmodule.NewDispatch(h.store.Connectors(), h.vault, registry, nil)
	if err != nil {
		t.Fatalf("New connector dispatcher: %v", err)
	}
	repairs, ok := h.store.DriverSteps().(store.TerminalDriverStepRepairStore)
	if !ok {
		t.Fatal("test DriverStep store lacks terminal repair support")
	}
	taskRunCommands := connectorE2ETaskRunCommands{}
	executionCapability, err := appserve.NewExecutionCapability(appserve.ExecutionDependencies{
		TaskRuns: h.store.TaskRuns(), DriverRuns: h.store.DriverRuns(), DriverSteps: h.store.DriverSteps(),
		TerminalStepRepairs: repairs, TaskRunEvents: h.store.TaskRunEvents(), Nodes: h.store.Nodes(),
		WorkerProfiles: h.store.WorkerProfiles(), AgentQueries: testutil.StaticAgentQueries{}, Outbox: h.store.Outbox(),
		Awaits: h.store.Awaits(), TriggerEvents: h.store.TriggerEvents(), Workspaces: h.store.Workspaces(),
		AtomicTaskRunRequests: taskRunCommands, AtomicTaskRunClaims: taskRunCommands,
		AtomicTaskRunWorkItemDesign: taskRunCommands,
		AtomicTaskRunRequeues:       taskRunCommands, AtomicTaskRunRetryExhaustion: taskRunCommands,
		AllowLegacyStoreAdapters: true,
	})
	if err != nil {
		t.Fatalf("new Execution capability: %v", err)
	}

	mux := http.NewServeMux()
	driverapi.NewModule(driverapi.Config{
		Store: h.store, Dispatcher: dispatcher, RunTokenKey: h.runTokenKey,
		Execution: executionCapability.DriverRunAPI(), ExecutionAuthorities: executionCapability.DriverRunAuthorityResolver(),
	}).Register(mux)
	workflow, err := webhookingestion.New(
		webhooks.NewCompatibilityVerifier(webhooks.CompatibilityVerifierConfig{
			Bindings: h.store.TriggerBindings(), Connectors: h.store.Connectors(),
		}),
		connectorE2EAuthorityProvider{}, connectorE2EAdmission{st: h.store},
	)
	if err != nil {
		t.Fatalf("new webhook ingestion workflow: %v", err)
	}
	webhooks.New(webhooks.Config{
		Workflow: workflow, Automation: connectorE2EQueries{st: h.store},
	}).Register(mux)
	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)
	return h
}

// provisionWorkspace performs the workspace-admin setup. The connector and
// grant writes go through the same ConnectorStore/ConnectorGrantStore surface
// the `loom connector create` / `loom connector grant create` CLI cores drive
// (internal/cli/connector), including sealing the outbound credential BEFORE
// the store write — stores never see plaintext.
func (h *e2eHarness) provisionWorkspace(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.store.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: e2eWorkspace, DriverID: "driver-1", Name: "pr-agent",
		OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := h.store.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: e2eWorkspace, VersionID: "version-1", DriverID: "driver-1", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := h.store.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: e2eWorkspace, BindingID: "binding-1", Name: "PR opened",
		SourceKind: "github", RouteKey: "github.pull_request.opened",
		DriverID: "driver-1", DriverVersionID: "version-1", Enabled: true,
		// Legacy per-binding secret: must STOP verifying once the workspace
		// holds a github connector (the connector becomes the root).
		WebhookSecret: legacyBindingSecret,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}

	sealed, err := h.vault.Seal([]byte(outboundCredentialV1), connectorsmodule.CredentialAAD(e2eWorkspace, "gh-main"))
	if err != nil {
		t.Fatalf("Seal credential: %v", err)
	}
	if _, err := h.store.Connectors().CreateConnectorRecord(ctx, connectorsmodule.CreateConnectorMutation{
		WorkspaceKey: e2eWorkspace, ConnectorID: "gh-main",
		SourceKind:               connectorsmodule.ConnectorSourceGitHub,
		DisplayName:              "GitHub (main)",
		InboundSecret:            inboundSecretV1,
		OutboundCredentialSealed: sealed,
		CreatedBy:                "tyson",
	}); err != nil {
		t.Fatalf("Create connector: %v", err)
	}

	// Deny-by-default: ONLY merge and pull_request.read on repo:octocat/hello
	// are granted. Everything else must refuse with grant_denied.
	for i, action := range []string{"github.merge", "github.pull_request.read"} {
		if _, err := h.store.Connectors().CreateManagementGrant(ctx, connectorsmodule.CreateGrantMutation{
			WorkspaceKey: e2eWorkspace, GrantID: fmt.Sprintf("grant-%d", i+1),
			ConnectorID: "gh-main", BindingID: "binding-1",
			Action: action, ResourcePattern: "repo:octocat/hello",
		}); err != nil {
			t.Fatalf("Create grant %s: %v", action, err)
		}
	}
}

func (h *e2eHarness) record(raw []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.responses = append(h.responses, raw)
}

func (h *e2eHarness) recordedResponses() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([][]byte(nil), h.responses...)
}

// webhookPayload is the pull_request.opened delivery body (route key
// github.pull_request.opened via the github adapter).
func webhookPayload() []byte {
	return []byte(`{
		"action": "opened",
		"number": 7,
		"pull_request": {"number": 7, "head": {"sha": "headsha-1"}, "base": {"ref": "main"}},
		"repository": {"full_name": "octocat/hello"},
		"sender": {"login": "octo-admin"}
	}`)
}

// postGitHubWebhook signs payload with secret (GitHub's sha256= HMAC scheme)
// and posts it to the production ingress route.
func (h *e2eHarness) postGitHubWebhook(t *testing.T, deliveryID, secret string, payload []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost,
		h.server.URL+"/api/workspaces/"+e2eWorkspace+"/webhooks/github", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new webhook request: %v", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return h.do(t, req)
}

// dispatchOp posts one connector-dispatch driver op with only the claimed
// run's bearer token, exactly as the current flue SDK does. The server derives
// the run/node/lease/fence owner tuple; no legacy identity header is sent.
func (h *e2eHarness) dispatchOp(t *testing.T, body map[string]any) (int, []byte) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal dispatch body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost,
		h.server.URL+"/api/workspaces/"+e2eWorkspace+"/driver/connector-dispatch", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new dispatch request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.runToken)
	return h.do(t, req)
}

// get issues a GET against the serve surface (audit/event listings), feeding
// the response into the leak scan like every other response.
func (h *e2eHarness) get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.server.URL+path, nil)
	if err != nil {
		t.Fatalf("new GET request: %v", err)
	}
	return h.do(t, req)
}

func (h *e2eHarness) do(t *testing.T, req *http.Request) (int, []byte) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", req.Method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	h.record(raw)
	return resp.StatusCode, raw
}

// claimRun claims the webhook-admitted run as a worker node would, giving the
// suite the run-scoped identity the workflow holds — and nothing else.
func (h *e2eHarness) claimRun(t *testing.T, runID string) {
	t.Helper()
	claimed, err := h.store.DriverRuns().Claim(context.Background(), e2eWorkspace, runID, "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim driver run: %v", err)
	}
	h.runID = claimed.RunID
	h.nodeID = claimed.NodeID
	h.leaseID = claimed.LeaseID
	h.fence = claimed.FencingToken
	leaseToken, err := driver.DeriveDriverRunLeaseToken(
		h.runTokenKey, e2eWorkspace, h.runID, h.nodeID, h.leaseID,
	)
	if err != nil {
		t.Fatalf("derive DriverRun lease token: %v", err)
	}
	h.runToken, err = driver.MintRunToken(driver.RunTokenClaims{
		WorkspaceKey: e2eWorkspace, RunID: h.runID, NodeID: h.nodeID,
		LeaseID: h.leaseID, FencingToken: h.fence,
	}, h.runTokenKey, time.Hour)
	if err != nil {
		t.Fatalf("mint DriverRun token: %v", err)
	}
	if leaseToken == "" {
		t.Fatal("derived DriverRun lease token is empty")
	}
}

// primaryRunID extracts deliveries[0].driver_run_id from a 202 dispatch body.
func primaryRunID(t *testing.T, raw []byte) string {
	t.Helper()
	var decoded struct {
		Deliveries []struct {
			DriverRunID string `json:"driver_run_id"`
		} `json:"deliveries"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode webhook response: %v (body %s)", err, raw)
	}
	if len(decoded.Deliveries) == 0 || decoded.Deliveries[0].DriverRunID == "" {
		t.Fatalf("webhook response carries no admitted run: %s", raw)
	}
	return decoded.Deliveries[0].DriverRunID
}

// decodeDispatch decodes the camelCase connector-dispatch success body.
func decodeDispatch(t *testing.T, raw []byte) (callID, decision string, status int, body map[string]any) {
	t.Helper()
	var decoded struct {
		CallID   string         `json:"callId"`
		Decision string         `json:"decision"`
		Status   int            `json:"status"`
		Body     map[string]any `json:"body"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode dispatch response: %v (body %s)", err, raw)
	}
	return decoded.CallID, decoded.Decision, decoded.Status, decoded.Body
}

// errorEnvelope decodes the structured driver-op error envelope.
func errorEnvelope(t *testing.T, raw []byte) (code string, retryable bool) {
	t.Helper()
	var decoded struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode error envelope: %v (body %s)", err, raw)
	}
	return decoded.Error.Code, decoded.Error.Retryable
}

// assertUniform401 pins the S2 uniform verification-failure response: one
// indistinguishable body for every verification-stage refusal.
func assertUniform401(t *testing.T, label string, status int, raw []byte) {
	t.Helper()
	if status != http.StatusUnauthorized {
		t.Fatalf("%s: status = %d, want 401 (body %s)", label, status, raw)
	}
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("%s: decode 401 body: %v (%s)", label, err, raw)
	}
	if decoded["error"] != "webhook signature verification failed" || len(decoded) != 1 {
		t.Fatalf("%s: 401 body = %s, want uniform verification-failure body", label, raw)
	}
}

// --- the end-to-end journey -----------------------------------------------

func TestConnectorEndToEnd(t *testing.T) {
	h := newE2EHarness(t)
	ctx := context.Background()

	// (1) Ingress: a delivery signed with the connector's inbound secret is
	// the ONLY one that verifies. The legacy binding secret and a tampered
	// signature both get the uniform 401.
	status, raw := h.postGitHubWebhook(t, "e2e-delivery-legacy", legacyBindingSecret, webhookPayload())
	assertUniform401(t, "legacy binding secret", status, raw)
	status, raw = h.postGitHubWebhook(t, "e2e-delivery-tampered", "completely-wrong-secret", webhookPayload())
	assertUniform401(t, "tampered signature", status, raw)

	status, raw = h.postGitHubWebhook(t, "e2e-delivery-1", inboundSecretV1, webhookPayload())
	if status != http.StatusAccepted {
		t.Fatalf("verified webhook: status = %d, want 202 (body %s)", status, raw)
	}
	runID := primaryRunID(t, raw)

	// The refused deliveries must not have admitted runs or persisted events.
	events, err := h.store.TriggerEvents().List(ctx, e2eWorkspace, store.TriggerEventFilter{})
	if err != nil {
		t.Fatalf("List trigger events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("trigger events = %d, want exactly the verified delivery", len(events))
	}

	// (2) The workflow claims its run and now holds only the run-scoped
	// driver identity — no vault key, no credential, no admin store handle.
	h.claimRun(t, runID)

	// (3) Deny-by-default: an ungranted action refuses with grant_denied,
	// journaled, and the provider is never contacted.
	status, raw = h.dispatchOp(t, map[string]any{
		"connectorId": "gh-main",
		"action":      "github.issue_comment.post",
		"resource":    "repo:octocat/hello",
		"args":        map[string]any{"owner": "octocat", "repo": "hello", "number": 7, "body": "hi"},
		"callSeq":     1,
	})
	if status != http.StatusForbidden {
		t.Fatalf("ungranted action: status = %d, want 403 (body %s)", status, raw)
	}
	if code, _ := errorEnvelope(t, raw); code != "grant_denied" {
		t.Fatalf("ungranted action: error code = %q, want grant_denied", code)
	}
	if calls, _ := h.github.snapshot(); len(calls) != 0 {
		t.Fatalf("upstream contacted %d times for a denied call, want 0", len(calls))
	}

	// (4) Stale subject: the head moves after the workflow pinned headsha-1;
	// GitHub's server-side sha precondition rejects the merge with 409 and NO
	// merge is applied.
	h.github.setHead("headsha-2")
	status, raw = h.dispatchOp(t, map[string]any{
		"connectorId":   "gh-main",
		"action":        "github.merge",
		"resource":      "repo:octocat/hello",
		"args":          map[string]any{"owner": "octocat", "repo": "hello", "number": 7},
		"preconditions": map[string]any{"expectedHeadSha": "headsha-1"},
		"callSeq":       2,
	})
	if status != http.StatusConflict {
		t.Fatalf("stale merge: status = %d, want 409 (body %s)", status, raw)
	}
	if code, retryable := errorEnvelope(t, raw); code != "stale_subject" || retryable {
		t.Fatalf("stale merge: code/retryable = %q/%v, want stale_subject/false", code, retryable)
	}
	if _, merged := h.github.snapshot(); merged {
		t.Fatal("stale merge applied the merge upstream; sha precondition must prevent it")
	}

	// (5) The workflow re-reads the PR (granted read action) to refresh the
	// head sha, then merges against the fresh pin: granted end to end.
	status, raw = h.dispatchOp(t, map[string]any{
		"connectorId": "gh-main",
		"action":      "github.pull_request.read",
		"resource":    "repo:octocat/hello",
		"args":        map[string]any{"owner": "octocat", "repo": "hello", "number": 7},
		"callSeq":     3,
	})
	if status != http.StatusOK {
		t.Fatalf("pull_request.read: status = %d, want 200 (body %s)", status, raw)
	}
	_, decision, _, body := decodeDispatch(t, raw)
	if decision != "granted" || body["headSha"] != "headsha-2" {
		t.Fatalf("pull_request.read = decision %q body %v, want granted with headSha=headsha-2", decision, body)
	}

	status, raw = h.dispatchOp(t, map[string]any{
		"connectorId":   "gh-main",
		"action":        "github.merge",
		"resource":      "repo:octocat/hello",
		"args":          map[string]any{"owner": "octocat", "repo": "hello", "number": 7},
		"preconditions": map[string]any{"expectedHeadSha": "headsha-2"},
		"callSeq":       4,
	})
	if status != http.StatusOK {
		t.Fatalf("fresh merge: status = %d, want 200 (body %s)", status, raw)
	}
	callID, decision, upstreamStatus, body := decodeDispatch(t, raw)
	wantMergeCallID := connectorsmodule.ConnectorCallID(h.runID, "github.merge", 4)
	if callID != wantMergeCallID || decision != "granted" || upstreamStatus != 200 || body["merged"] != true {
		t.Fatalf("fresh merge = %s/%s/%d/%v, want %s/granted/200/merged",
			callID, decision, upstreamStatus, body, wantMergeCallID)
	}
	if _, merged := h.github.snapshot(); !merged {
		t.Fatal("fresh merge did not reach the upstream")
	}

	// (6) Credential boundary: the v1 credential appeared ONLY in the
	// Authorization header presented to the upstream, on every egress call,
	// each fenced by its deterministic runID#action#seq idempotency key.
	calls, _ := h.github.snapshot()
	if len(calls) != 3 {
		t.Fatalf("upstream calls = %d, want 3 (stale merge, read, fresh merge)", len(calls))
	}
	wantKeys := []string{
		connectorsmodule.ConnectorCallID(h.runID, "github.merge", 2),
		connectorsmodule.ConnectorCallID(h.runID, "github.pull_request.read", 3),
		connectorsmodule.ConnectorCallID(h.runID, "github.merge", 4),
	}
	for i, call := range calls {
		if call.Authorization != "Bearer "+outboundCredentialV1 {
			t.Fatalf("upstream call %d authorization = %q, want the unsealed v1 credential", i, call.Authorization)
		}
		if call.IdempotencyKey != wantKeys[i] {
			t.Fatalf("upstream call %d idempotency key = %q, want %q", i, call.IdempotencyKey, wantKeys[i])
		}
	}

	// (7) Audit journal: one row per dispatch — denied AND granted AND stale —
	// all attributed to the server-resolved binding, never caller-supplied.
	assertAuditTrail(t, h, map[string]auditExpectation{
		connectorsmodule.ConnectorCallID(h.runID, "github.issue_comment.post", 1): {connectorsmodule.ConnectorCallDenied, 0},
		connectorsmodule.ConnectorCallID(h.runID, "github.merge", 2):              {connectorsmodule.ConnectorCallStaleSubject, 409},
		connectorsmodule.ConnectorCallID(h.runID, "github.pull_request.read", 3):  {connectorsmodule.ConnectorCallGranted, 200},
		connectorsmodule.ConnectorCallID(h.runID, "github.merge", 4):              {connectorsmodule.ConnectorCallGranted, 200},
	})

	// (8) Mid-flight rotation: both secrets rotate while the run is live.
	management, err := connectorsmodule.NewManagementWithSecrets(h.store.Connectors(), h.vault, time.Now)
	if err != nil {
		t.Fatalf("New connector management for rotation: %v", err)
	}
	rotated, err := management.RotateConnector(ctx,
		connectorsmodule.RotateConnectorCommand{
			WorkspaceKey:     e2eWorkspace,
			ConnectorID:      "gh-main",
			NewInboundSecret: inboundSecretV2,
			NewCredential:    []byte(outboundCredentialV2),
			InboundWindow:    time.Hour,
		})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated.PreviousSecretValidUntil == nil || rotated.RotatedAt == nil {
		t.Fatal("rotation recorded no dual-secret window")
	}

	// Inside the window BOTH inbound secrets verify; garbage still refuses.
	if status, raw = h.postGitHubWebhook(t, "e2e-delivery-2", inboundSecretV1, webhookPayload()); status != http.StatusAccepted {
		t.Fatalf("previous inbound secret inside window: status = %d, want 202 (body %s)", status, raw)
	}
	if status, raw = h.postGitHubWebhook(t, "e2e-delivery-3", inboundSecretV2, webhookPayload()); status != http.StatusAccepted {
		t.Fatalf("new inbound secret: status = %d, want 202 (body %s)", status, raw)
	}
	status, raw = h.postGitHubWebhook(t, "e2e-delivery-4", "still-wrong-secret", webhookPayload())
	assertUniform401(t, "wrong secret after rotation", status, raw)

	// The very next egress call unseals the NEW credential — no grace window
	// for the old outbound token.
	status, raw = h.dispatchOp(t, map[string]any{
		"connectorId": "gh-main",
		"action":      "github.pull_request.read",
		"resource":    "repo:octocat/hello",
		"args":        map[string]any{"owner": "octocat", "repo": "hello", "number": 7},
		"callSeq":     5,
	})
	if status != http.StatusOK {
		t.Fatalf("post-rotation read: status = %d, want 200 (body %s)", status, raw)
	}
	calls, _ = h.github.snapshot()
	if got := calls[len(calls)-1].Authorization; got != "Bearer "+outboundCredentialV2 {
		t.Fatalf("post-rotation authorization = %q, want the rotated credential", got)
	}

	// (9) The §9.5 scan: every HTTP response the serve surface produced, the
	// audit/event listings, every journal row, and the redacted connector
	// record — zero secret material anywhere.
	h.get(t, "/api/workspaces/"+e2eWorkspace+"/trigger-events")
	h.get(t, "/api/workspaces/"+e2eWorkspace+"/trigger-deliveries")
	assertNoSecretMaterial(t, h)
}

// auditExpectation pins one journal row's decision and upstream status.
type auditExpectation struct {
	decision connectorsmodule.ConnectorCallDecision
	status   int
}

func assertAuditTrail(t *testing.T, h *e2eHarness, want map[string]auditExpectation) {
	t.Helper()
	records, err := h.store.Connectors().ListCallRecordsByRun(context.Background(), e2eWorkspace, h.runID, connectorsmodule.ConnectorCallFilter{})
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	if len(records) != len(want) {
		t.Fatalf("audit rows = %d, want %d", len(records), len(want))
	}
	for _, rec := range records {
		expected, ok := want[rec.CallID]
		if !ok {
			t.Fatalf("unexpected audit row %q", rec.CallID)
		}
		if rec.Decision != expected.decision || rec.UpstreamStatus != expected.status {
			t.Fatalf("audit row %q = %s/%d, want %s/%d",
				rec.CallID, rec.Decision, rec.UpstreamStatus, expected.decision, expected.status)
		}
		if rec.BindingID != "binding-1" || rec.ConnectorID != "gh-main" || rec.RunID != h.runID {
			t.Fatalf("audit row %q attribution = %s/%s/%s, want binding-1/gh-main/%s",
				rec.CallID, rec.BindingID, rec.ConnectorID, rec.RunID, h.runID)
		}
	}
}

// assertNoSecretMaterial scans every captured HTTP response, every connector
// journal row (run-scoped AND rotation rows), and the redacted connector
// record for every secret string the suite holds.
func assertNoSecretMaterial(t *testing.T, h *e2eHarness) {
	t.Helper()
	ctx := context.Background()

	blobs := map[string][]byte{}
	for i, resp := range h.recordedResponses() {
		blobs[fmt.Sprintf("http response %d", i)] = resp
	}

	runRows, err := h.store.Connectors().ListCallRecordsByRun(ctx, e2eWorkspace, h.runID, connectorsmodule.ConnectorCallFilter{})
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	rotationRows, err := h.store.Connectors().ListCallRecordsByBinding(ctx, e2eWorkspace, connectorsmodule.RotationAuditBindingID, connectorsmodule.ConnectorCallFilter{})
	if err != nil {
		t.Fatalf("ListByBinding rotation rows: %v", err)
	}
	if len(rotationRows) != 1 {
		t.Fatalf("rotation audit rows = %d, want 1", len(rotationRows))
	}
	for i, rec := range append(runRows, rotationRows...) {
		encoded, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal audit row: %v", err)
		}
		blobs[fmt.Sprintf("audit row %d (%s)", i, rec.CallID)] = encoded
	}

	conn, err := h.store.Connectors().GetConnectorRecord(ctx, e2eWorkspace, "gh-main")
	if err != nil {
		t.Fatalf("Get connector: %v", err)
	}
	encoded, err := json.Marshal(conn)
	if err != nil {
		t.Fatalf("marshal connector: %v", err)
	}
	blobs["redacted connector record"] = encoded

	for label, blob := range blobs {
		for _, secret := range e2eSecrets {
			if strings.Contains(string(blob), secret) {
				t.Fatalf("§9.5 violation: %s leaks %q: %s", label, secret, blob)
			}
		}
	}
}

// --- §9.5 workflow environment scan ----------------------------------------

// TestConnectorE2EWorkflowEnvNeverSeesSecrets spawns a workflow process
// through the REAL NodeRunner env seam (driver/env.go allowlist filtering)
// with a stub runtime that dumps its environment, then asserts the vault key
// and credential-bearing host vars were filtered while the run-scoped driver
// identity — the only credential a workflow may hold — came through.
func TestConnectorE2EWorkflowEnvNeverSeesSecrets(t *testing.T) {
	vaultKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	t.Setenv(connectorsvault.VaultKeyEnvVar, vaultKey)
	// An operator host env holding the raw upstream credential must never
	// reach the workflow either (sensitive-name filtering).
	t.Setenv("GITHUB_TOKEN", outboundCredentialV1)
	t.Setenv("LOOM_E2E_INBOUND_SECRET", inboundSecretV1)

	bundleRoot := t.TempDir()
	stub := filepath.Join(t.TempDir(), "fake-node")
	script := "#!/bin/sh\nenv > \"$PWD/workflow-env.txt\"\nprintf '{\"status\":\"completed\",\"summary\":\"env probe\"}\\n'\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil { //nolint:gosec // test stub must be executable
		t.Fatalf("write stub runtime: %v", err)
	}

	runner := driver.NodeRunner{
		NodePath:        stub,
		ExecTaskCommand: []string{"loom-exec-task-stub"}, // explicit per the loomExecutablePath lesson
		APIBaseURL:      "http://127.0.0.1:1",
		APIToken:        "run-scoped-driver-token",
	}
	res, err := runner.Run(context.Background(), driver.RunRequest{
		Run: &domain.DriverRun{
			WorkspaceKey: e2eWorkspace, RunID: "run-env-probe", DriverID: "driver-1",
			NodeID: "node-1", LeaseID: "lease-1", FencingToken: 7,
		},
		BundleRoot: bundleRoot,
		ServerPath: filepath.Join(bundleRoot, "flue-server.mjs"),
		// The env seam is under test, not the SB3 trust gate: a trusted
		// request passes the process launcher.
		TrustLevel: workflowcatalog.DriverTrustTrusted,
	})
	if err != nil {
		t.Fatalf("NodeRunner.Run: %v", err)
	}
	if res.Status != domain.DriverRunCompleted {
		t.Fatalf("stub run status = %q (%s), want completed", res.Status, res.Summary)
	}

	dump, err := os.ReadFile(filepath.Join(bundleRoot, "workflow-env.txt"))
	if err != nil {
		t.Fatalf("read workflow env dump: %v", err)
	}
	env := string(dump)

	// Positive controls: the dump is the real spawned env and carries the
	// run-scoped identity + driver API token the workflow legitimately holds.
	for _, want := range []string{
		"LOOM_DRIVER_RUN_ID=run-env-probe",
		"LOOM_DRIVER_API_URL=http://127.0.0.1:1",
		"LOOM_DRIVER_API_TOKEN=run-scoped-driver-token",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("workflow env missing %q:\n%s", want, env)
		}
	}

	// The §9.5 assertion: no vault key, no credential, no inbound secret —
	// neither the values nor the secret-bearing variables themselves.
	for _, forbidden := range []string{
		vaultKey,
		connectorsvault.VaultKeyEnvVar,
		outboundCredentialV1,
		inboundSecretV1,
		"GITHUB_TOKEN",
		"LOOM_E2E_INBOUND_SECRET",
	} {
		if strings.Contains(env, forbidden) {
			t.Fatalf("§9.5 violation: workflow env contains %q:\n%s", forbidden, env)
		}
	}
}
