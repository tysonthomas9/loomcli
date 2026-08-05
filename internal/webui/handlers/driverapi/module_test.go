package driverapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

// fakeIssueBackend embeds the interface so only the methods the driver ops
// touch need real implementations; anything else panics loudly.
type fakeIssueBackend struct {
	backend.IssueBackend
	ready     []backend.IssueData
	readyOpts []backend.ReadyOpts
	blocked   []backend.IssueData
	children  []backend.IssueData
	epic      *backend.IssueDetailData
	actor     string
	claimed   []string
	releases  []fakeRelease
}

type fakeRelease struct {
	id    string
	actor string
}

// testWorkflowEventAuthorityProvider and testLegacyEventAdmission are
// deliberately test-only compatibility wiring for the pre-Phase-3 memstore
// route suites. Production driverapi has no InternalSource or TriggerRoutes
// fallback.
type testWorkflowEventAuthorityProvider struct{}

func (testWorkflowEventAuthorityProvider) AuthorityForVerifiedRun(context.Context, workfloweventing.VerifiedRun) (authority.ExecutionAuthority, error) {
	return authority.ExecutionAuthority{}, nil
}

type testLegacyEventAdmission struct{ st store.Store }

func (adapter testLegacyEventAdmission) AdmitEvent(ctx context.Context, _ automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
	parent, err := adapter.st.DriverRuns().Get(ctx, command.WorkspaceKey, "run-1")
	if err != nil {
		return nil, err
	}
	sourceResult, err := (&trigger.InternalSource{Store: adapter.st}).Emit(ctx, command.WorkspaceKey, trigger.InternalEvent{
		EventID: command.SourceEventID, EventType: command.EventType,
		Origin: domain.TriggerEventOriginWorkflow, ParentEventID: parent.SourceRef,
		EmittedByRunID: parent.RunID, SubjectRef: command.SubjectRef,
		ActorRef: driverpkg.DriverRunActor(parent.RunID),
		EpicID:   firstNonEmpty(parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload)),
		Payload:  command.Payload, SubjectAttrs: command.SubjectAttrs,
	})
	if err != nil {
		return nil, err
	}
	result := &automation.AdmissionResult{
		Dropped: sourceResult.Dropped, DropReason: sourceResult.DropReason,
		EventType: sourceResult.EventType, RouteKey: sourceResult.RouteKey,
		Origin: sourceResult.Origin, HopDepth: sourceResult.HopDepth,
	}
	if sourceResult.Dropped {
		return result, nil
	}
	result.Event = &automation.Event{
		WorkspaceKey: command.WorkspaceKey, SourceKind: automation.SourceKindInternal,
		SourceEventID: command.SourceEventID, EventType: sourceResult.EventType,
		RouteKey: sourceResult.RouteKey, SubjectRef: command.SubjectRef,
		ActorRef: driverpkg.DriverRunActor(parent.RunID), EmittingRunID: parent.RunID,
		ParentEventID: parent.SourceRef, EpicID: firstNonEmpty(parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload)),
		Origin: sourceResult.Origin, HopDepth: sourceResult.HopDepth,
	}
	if sourceResult.Dispatch != nil {
		result.Deliveries = make([]*automation.Delivery, 0, len(sourceResult.Dispatch.Deliveries))
		for _, delivery := range sourceResult.Dispatch.Deliveries {
			result.Deliveries = append(result.Deliveries, &automation.Delivery{
				DeliveryID: delivery.DeliveryID, TriggerBindingID: delivery.BindingID,
				DriverRunID: delivery.RunID, Status: delivery.Status,
				RejectionReason: delivery.RejectionReason,
			})
		}
	}
	return result, nil
}

func (f *fakeIssueBackend) Ready(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	f.readyOpts = append(f.readyOpts, opts)
	return f.ready, nil
}

func (f *fakeIssueBackend) ReleaseIssueAsActor(_ context.Context, id, actor string) error {
	f.releases = append(f.releases, fakeRelease{id: id, actor: actor})
	return nil
}

func (f *fakeIssueBackend) Blocked(_ context.Context, _ backend.BlockedOpts) ([]backend.IssueData, error) {
	return f.blocked, nil
}

func (f *fakeIssueBackend) List(_ context.Context, _ backend.ListOpts) ([]backend.IssueData, error) {
	return f.children, nil
}

func (f *fakeIssueBackend) ClaimIssue(_ context.Context, id string, _ time.Duration) error {
	f.claimed = append(f.claimed, id)
	return nil
}

func (f *fakeIssueBackend) ClaimIssueAsActor(_ context.Context, id string, _ time.Duration, actor string) error {
	f.claimed = append(f.claimed, id)
	f.actor = actor
	return nil
}

func (f *fakeIssueBackend) Get(_ context.Context, _ string) (*backend.IssueDetailData, error) {
	return f.epic, nil
}

type testHarness struct {
	server      *httptest.Server
	store       store.Store
	module      *Module
	backend     *fakeIssueBackend
	runID       string
	nodeID      string
	leaseID     string
	fence       int64
	runTokenKey []byte
}

func newTestHarness(t *testing.T, apiToken string) *testHarness {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "driver-1",
		Name:         "epic-runner",
		OwnerType:    domain.DriverOwnerSystem,
		Status:       domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "WS",
		VersionID:        "version-1",
		DriverID:         "driver-1",
		Version:          1,
		SourceDigest:     "sha256:source",
		BundleDigest:     "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-1",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		EpicID:          "EPIC-1",
	}); err != nil {
		t.Fatalf("Create driver run: %v", err)
	}
	claimed, err := st.DriverRuns().Claim(ctx, "WS", "run-1", "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim driver run: %v", err)
	}
	fake := &fakeIssueBackend{}
	// Every harness carries a run-token signing key so all existing
	// header-quad/static-bearer tests double as proof the legacy path is
	// unchanged when the token auth path is enabled.
	runTokenKey := bytes.Repeat([]byte{0x42}, 32)
	eventWorkflow, err := workfloweventing.New(testWorkflowEventAuthorityProvider{}, testLegacyEventAdmission{st: st})
	if err != nil {
		t.Fatalf("new test workflow eventing: %v", err)
	}
	module := NewModule(Config{
		Store:            st,
		APIToken:         apiToken,
		RunTokenKey:      runTokenKey,
		WorkflowEventing: eventWorkflow,
		IssueBackends: func(_, actor string) (backend.IssueBackend, error) {
			fake.actor = actor
			return fake, nil
		},
	})
	mux := http.NewServeMux()
	module.Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &testHarness{
		server:      server,
		store:       st,
		module:      module,
		backend:     fake,
		runID:       claimed.RunID,
		nodeID:      claimed.NodeID,
		leaseID:     claimed.LeaseID,
		fence:       claimed.FencingToken,
		runTokenKey: runTokenKey,
	}
}

type opRequest struct {
	op      string
	body    any
	headers map[string]string
}

func (h *testHarness) do(t *testing.T, req opRequest) (*http.Response, map[string]any) {
	resp, decoded := h.doAny(t, req)
	asMap, _ := decoded.(map[string]any)
	return resp, asMap
}

func (h *testHarness) doAny(t *testing.T, req opRequest) (*http.Response, any) {
	t.Helper()
	payload, err := json.Marshal(req.body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	if req.body == nil {
		payload = []byte("{}")
	}
	httpReq, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/workspaces/WS/driver/"+req.op, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for name, value := range req.headers {
		if value != "" {
			httpReq.Header.Set(name, value)
		}
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var decoded any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("decode response: %v", err)
	}
	return resp, decoded
}

func (h *testHarness) ownerHeaders() map[string]string {
	return map[string]string{
		HeaderDriverRunID:        h.runID,
		HeaderDriverNodeID:       h.nodeID,
		HeaderDriverLeaseID:      h.leaseID,
		HeaderDriverFencingToken: fmt.Sprintf("%d", h.fence),
	}
}

func errorCode(t *testing.T, decoded map[string]any) string {
	t.Helper()
	envelope, ok := decoded["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no structured error: %v", decoded)
	}
	code, _ := envelope["code"].(string)
	return code
}

func TestDriverAPIRequiresRunIDHeader(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{op: "list-agents"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "unauthenticated" {
		t.Fatalf("error code = %q, want unauthenticated", code)
	}
}

func TestDriverAPIBearerToken(t *testing.T) {
	h := newTestHarness(t, "secret-token")

	headers := h.ownerHeaders()
	resp, decoded := h.do(t, opRequest{op: "list-agents", headers: headers})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want 401", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "unauthenticated" {
		t.Fatalf("error code = %q, want unauthenticated", code)
	}

	headers["Authorization"] = "Bearer wrong"
	resp, _ = h.do(t, opRequest{op: "list-agents", headers: headers})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status with wrong token = %d, want 401", resp.StatusCode)
	}

	headers["Authorization"] = "Bearer secret-token"
	resp, _ = h.do(t, opRequest{op: "list-agents", headers: headers})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status with correct token = %d, want 200", resp.StatusCode)
	}
}

func TestDriverAPIUnknownOp(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{op: "no-such-op", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "unknown_op" {
		t.Fatalf("error code = %q, want unknown_op", code)
	}
}

func TestDriverAPIRejectsForeignOwnerCredentials(t *testing.T) {
	h := newTestHarness(t, "")
	headers := h.ownerHeaders()
	headers[HeaderDriverFencingToken] = "999999"
	resp, decoded := h.do(t, opRequest{op: "active-task-runs", headers: headers})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "not_owner" {
		t.Fatalf("error code = %q, want not_owner", code)
	}
}

func TestDriverAPIRecoverStaleTasksRequiresOwnership(t *testing.T) {
	h := newTestHarness(t, "")

	// Missing owner credentials must not be able to fail this run's tasks.
	resp, decoded := h.do(t, opRequest{
		op:      "recover-stale-tasks",
		headers: map[string]string{HeaderDriverRunID: h.runID},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status without owner creds = %d, want 403", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "not_owner" {
		t.Fatalf("error code = %q, want not_owner", code)
	}

	resp, _ = h.do(t, opRequest{op: "recover-stale-tasks", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status with owner creds = %d, want 200", resp.StatusCode)
	}
}

func TestDriverAPIClaimReady(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.ready = []backend.IssueData{{ID: "TASK-7", Title: "do the thing"}}

	resp, decoded := h.do(t, opRequest{op: "claim-ready", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if decoded["id"] != "TASK-7" {
		t.Fatalf("claimed id = %v, want TASK-7", decoded["id"])
	}
	if decoded["claimedBy"] != "driver-run:run-1" {
		t.Fatalf("claimedBy = %v, want driver-run:run-1", decoded["claimedBy"])
	}
	if len(h.backend.claimed) != 1 || h.backend.claimed[0] != "TASK-7" {
		t.Fatalf("backend claims = %v, want [TASK-7]", h.backend.claimed)
	}
}

// TestDriverAPIClaimReadyThreadsTypeFilter proves the claim-ready `type` param
// reaches the ready view server-side (ITEM 3): the op decodes it and threads it
// into ReadyOpts.Type so a caller can claim only, e.g., bugs.
func TestDriverAPIClaimReadyThreadsTypeFilter(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.ready = []backend.IssueData{{ID: "BUG-1", IssueType: "bug"}}

	resp, decoded := h.do(t, opRequest{
		op:      "claim-ready",
		body:    map[string]any{"type": "bug"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if decoded["id"] != "BUG-1" {
		t.Fatalf("claimed id = %v, want BUG-1", decoded["id"])
	}
	if len(h.backend.readyOpts) != 1 || h.backend.readyOpts[0].Type != "bug" {
		t.Fatalf("ready opts = %+v, want the type=bug filter threaded to the ready view", h.backend.readyOpts)
	}
}

// TestDriverAPIClaimTaskIgnoresBodyActor is the ITEM 1 security regression:
// presenting a victim's actor label in the claim body must NOT key the lock by
// that label. The lock actor is always derived from the verified run, so a run
// can only ever claim under its own lease — no cross-agent lock takeover.
func TestDriverAPIClaimTaskIgnoresBodyActor(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.ready = []backend.IssueData{{ID: "TASK-7"}}

	resp, decoded := h.do(t, opRequest{
		op:      "claim-task",
		body:    map[string]any{"taskId": "TASK-7", "actor": "driver-run:victim"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if decoded["claimedBy"] != "driver-run:run-1" {
		t.Fatalf("claimedBy = %v, want the derived run actor (body actor must be ignored)", decoded["claimedBy"])
	}
	if h.backend.actor != "driver-run:run-1" {
		t.Fatalf("lock actor = %q, want driver-run:run-1 (body actor driver-run:victim must not key the lock)", h.backend.actor)
	}
}

// TestDriverAPIReleaseTaskIgnoresBodyActor is the release half of ITEM 1: a run
// cannot present a victim's actor and release a lock it never held. The release
// ownership actor is always the run's derived actor, so failure-recovery stays
// symmetric with the claim path (same run -> same actor) while cross-agent
// theft is impossible.
func TestDriverAPIReleaseTaskIgnoresBodyActor(t *testing.T) {
	h := newTestHarness(t, "")

	resp, _ := h.do(t, opRequest{
		op:      "release-task",
		body:    map[string]any{"taskId": "TASK-7", "actor": "driver-run:victim"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(h.backend.releases) != 1 || h.backend.releases[0].actor != "driver-run:run-1" {
		t.Fatalf("release calls = %+v, want one release keyed by the derived run actor (body actor ignored)", h.backend.releases)
	}
}

func TestDriverAPIEpicGet(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.epic = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "EPIC-1", Title: "epic"}}

	resp, decoded := h.do(t, opRequest{op: "epic-get", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if decoded["id"] != "EPIC-1" {
		t.Fatalf("epic id = %v, want EPIC-1", decoded["id"])
	}
}

func TestDriverAPIActiveTaskRunsEmpty(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{op: "active-task-runs", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if decoded["driverRunId"] != "run-1" {
		t.Fatalf("driverRunId = %v, want run-1", decoded["driverRunId"])
	}
	if count, ok := decoded["activeCount"].(float64); !ok || count != 0 {
		t.Fatalf("activeCount = %v, want 0", decoded["activeCount"])
	}
}

func TestDriverAPIExecTaskEnqueueUnschedulable(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{
		op: "exec-task",
		body: map[string]any{
			"taskId":          "TASK-9",
			"taskRunId":       "task-run-unschedulable",
			"providerProfile": "local-noop",
			"enqueueOnly":     true,
		},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "unschedulable" {
		t.Fatalf("error code = %q, want unschedulable", code)
	}
	envelope := decoded["error"].(map[string]any)
	if retryable, _ := envelope["retryable"].(bool); !retryable {
		t.Fatalf("retryable = %v, want true", envelope["retryable"])
	}
	children, err := h.store.TaskRuns().List(context.Background(), "WS", store.TaskRunFilter{DriverRunID: h.runID})
	if err != nil {
		t.Fatalf("List children: %v", err)
	}
	if len(children) != 0 {
		t.Fatalf("children = %+v, want none for unschedulable enqueue", children)
	}
}

func TestDriverAPITaskRunGetNotFound(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{
		op:      "task-run-get",
		body:    map[string]string{"taskRunId": "missing-run"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "not_found" {
		t.Fatalf("error code = %q, want not_found", code)
	}
}

func TestDriverAPIInvalidParams(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{
		op:      "deliver-agent-message",
		body:    map[string]string{"agent": "", "message": ""},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "invalid" {
		t.Fatalf("error code = %q, want invalid", code)
	}
}
