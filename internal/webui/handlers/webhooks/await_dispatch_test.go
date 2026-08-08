// Integration tests for the dispatch-time await matcher hook (chunk AW7):
// a signed webhook driven through the real httptest mux + memstore wiring
// resumes a pending run whose await matches the event's rendered subject key.
package webhooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// awaitE2ESuspendedRun creates, claims and suspends a run pending on the given
// await pattern, returning the await instance key.
func awaitE2ESuspendedRun(t *testing.T, st *memstore.Store, runID, pattern string, actorAllow []string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: routerE2EWS, RunID: runID, DriverID: "pr-review", DriverVersionID: "v1", Entrypoint: "run",
	}); err != nil {
		t.Fatalf("Create run: %v", err)
	}
	run, err := st.DriverRuns().Claim(ctx, routerE2EWS, runID, "node-1", "lease-"+runID)
	if err != nil {
		t.Fatalf("Claim run: %v", err)
	}
	key := domain.AwaitInstanceKey(runID, 1)
	res, err := st.Awaits().RegisterAwaitAndCheck(ctx, routerE2EWS, store.AwaitRegistration{
		InstanceKey: key, RunID: runID, Pattern: pattern, ActorAllow: actorAllow,
		Deadline: time.Now().Add(time.Hour),
	})
	if err != nil || res.Satisfied {
		t.Fatalf("RegisterAwaitAndCheck = %+v, %v; want pending", res, err)
	}
	if _, err := st.DriverRuns().Suspend(ctx, routerE2EWS, runID,
		run.NodeID, run.LeaseID, run.FencingToken, key); err != nil {
		t.Fatalf("Suspend run: %v", err)
	}
	return key
}

func awaitE2EPendingComposition(t *testing.T, st *memstore.Store, parentRunID, childRunID string) string {
	t.Helper()
	ctx := t.Context()
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: routerE2EWS, RunID: parentRunID,
		DriverID: "pr-review", DriverVersionID: "v1", Entrypoint: "run",
	}); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	parent, err := st.DriverRuns().Claim(ctx, routerE2EWS, parentRunID, "node-parent", "lease-parent")
	if err != nil {
		t.Fatalf("claim parent: %v", err)
	}
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: routerE2EWS, RunID: childRunID,
		DriverID: "pr-review", DriverVersionID: "v1", Entrypoint: "run",
		ParentRunID: parentRunID, SourceKind: driver.ChildRunSourceKind, SourceRef: parentRunID,
	}); err != nil {
		t.Fatalf("create child: %v", err)
	}
	key := domain.AwaitInstanceKey(parentRunID, 1)
	registered, err := st.Awaits().RegisterAwaitAndCheck(ctx, routerE2EWS, store.AwaitRegistration{
		InstanceKey: key, RunID: parentRunID, Pattern: driver.RunFinishedSubjectKey(childRunID),
		ActorAllow: []string{driver.RunFinishedActor}, Deadline: time.Now().Add(time.Minute),
	})
	if err != nil || registered == nil || registered.Satisfied {
		t.Fatalf("RegisterAwaitAndCheck = %+v, %v; want pending", registered, err)
	}
	if _, err := st.DriverRuns().Suspend(ctx, routerE2EWS, parentRunID,
		parent.NodeID, parent.LeaseID, parent.FencingToken, key); err != nil {
		t.Fatalf("Suspend composition parent: %v", err)
	}
	return key
}

func postSignedRunFinishedSpoof(t *testing.T, mux *http.ServeMux, deliveryID, childRunID string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Appendf(nil, `{"repository":{"full_name":%q},"sender":{"login":"system"}}`, childRunID)
	req := httptest.NewRequest(http.MethodPost,
		"/api/workspaces/"+routerE2EWS+"/webhooks/github", bytes.NewReader(body))
	req.Header.Set(githubEventHeader, driver.RunFinishedEventType)
	req.Header.Set(githubDeliveryHeader, deliveryID)
	req.Header.Set(githubSignatureHeader, githubSignature(routerE2ESecret, body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("signed run.finished spoof status = %d, body = %s; want 400", rr.Code, rr.Body.String())
	}
	return rr
}

func assertPendingComposition(t *testing.T, st *memstore.Store, parentRunID, childRunID, instanceKey string) {
	t.Helper()
	run, err := st.DriverRuns().Get(t.Context(), routerE2EWS, parentRunID)
	if err != nil || run.Status != domain.DriverRunSuspendedAwaitingEvent || run.ResumeSourceEventID != "" {
		t.Fatalf("parent after spoof = %+v, %v; want suspended without resume source", run, err)
	}
	if _, err := st.Awaits().GetSatisfiedAwait(t.Context(), routerE2EWS, instanceKey); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("satisfied await after spoof = %v; want not found", err)
	}
	pending, err := st.Awaits().ListAwaitsByPattern(
		t.Context(), routerE2EWS, driver.RunFinishedSubjectKey(childRunID),
	)
	if err != nil || len(pending) != 1 || pending[0].InstanceKey != instanceKey {
		t.Fatalf("pending composition awaits = %+v, %v; want %s", pending, err, instanceKey)
	}
}

func TestSignedWebhookRunFinishedSpoofBeforeCompositionRegistrationIsRejected(t *testing.T) {
	st := routerE2EStore(t)
	routerE2EBinding(t, st, store.TriggerBindingCreate{
		BindingID: "b-run-finished-future", RouteKey: "github." + driver.RunFinishedEventType,
		WebhookSecret: routerE2ESecret,
	})
	mux := routerE2EMux(st)
	postSignedRunFinishedSpoof(t, mux, "spoof-before", "child-future")
	if events, runs, deliveries := routerE2ECounts(t, st); events != 0 || runs != 0 || deliveries != 0 {
		t.Fatalf("signed preseed created state: events=%d runs=%d deliveries=%d", events, runs, deliveries)
	}

	key := awaitE2EPendingComposition(t, st, "parent-future", "child-future")
	assertPendingComposition(t, st, "parent-future", "child-future", key)
	if events, runs, deliveries := routerE2ECounts(t, st); events != 0 || runs != 2 || deliveries != 0 {
		t.Fatalf("post-registration state: events=%d runs=%d deliveries=%d; want 0/2/0", events, runs, deliveries)
	}
}

func TestSignedWebhookRunFinishedSpoofAgainstPendingCompositionIsRejected(t *testing.T) {
	st := routerE2EStore(t)
	routerE2EBinding(t, st, store.TriggerBindingCreate{
		BindingID: "b-run-finished-pending", RouteKey: "github." + driver.RunFinishedEventType,
		WebhookSecret: routerE2ESecret,
	})
	key := awaitE2EPendingComposition(t, st, "parent-pending", "child-pending")
	postSignedRunFinishedSpoof(t, routerE2EMux(st), "spoof-pending", "child-pending")
	assertPendingComposition(t, st, "parent-pending", "child-pending", key)
	if events, runs, deliveries := routerE2ECounts(t, st); events != 0 || runs != 2 || deliveries != 0 {
		t.Fatalf("signed pending spoof created state: events=%d runs=%d deliveries=%d", events, runs, deliveries)
	}
}

// TestWebhookDispatchResumesAwaitingRun: the full ingress path — signed
// webhook -> router fan-out -> await matcher -> suspended run re-queued with
// the event payload persisted on the satisfied row.
func TestWebhookDispatchResumesAwaitingRun(t *testing.T) {
	st := routerE2EStore(t)
	routerE2EBinding(t, st, store.TriggerBindingCreate{
		BindingID: "b-await", RouteKey: "github.pull_request.opened", WebhookSecret: routerE2ESecret,
	})
	// The await pattern is the rendered event key: adapter event type plus
	// the normalized subject ref (RULE 1, exact equality).
	key := awaitE2ESuspendedRun(t, st, "run-waiter",
		domain.AwaitEventKey("pull_request", "acme/widgets#7"), []string{"octocat"})

	mux := routerE2EMux(st)
	body := routerE2EPRBody("opened", "sha-await")
	resp := routerE2EPost(t, mux, "await-del-1", body)
	if resp.Status != "accepted" || len(resp.Deliveries) != 1 {
		t.Fatalf("webhook response = %+v, want one accepted delivery", resp)
	}

	ctx := context.Background()
	run, err := st.DriverRuns().Get(ctx, routerE2EWS, "run-waiter")
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != "await-del-1" {
		t.Fatalf("run = %s resumed by %q, want queued by await-del-1", run.Status, run.ResumeSourceEventID)
	}
	satisfied, err := st.Awaits().GetSatisfiedAwait(ctx, routerE2EWS, key)
	if err != nil {
		t.Fatalf("GetSatisfiedAwait: %v", err)
	}
	if satisfied.Status != domain.AwaitSatisfied || satisfied.SatisfiedByEventID != "await-del-1" ||
		!bytes.Equal(satisfied.SatisfiedPayload, body) {
		t.Fatalf("satisfied row = %+v, want webhook body inline", satisfied)
	}
	// The admitted fan-out leg is untouched by the matcher pass.
	if resp.Deliveries[0].Status != automation.DeliveryDispatched {
		t.Fatalf("delivery = %+v, want dispatched", resp.Deliveries[0])
	}
}

// TestWebhookDispatchActorRejectedNeverResumes: vet A3 — an event whose
// actor fails the await's allow-list is accepted (202, fan-out intact) but
// never resolves or resumes the pending run.
func TestWebhookDispatchActorRejectedNeverResumes(t *testing.T) {
	st := routerE2EStore(t)
	routerE2EBinding(t, st, store.TriggerBindingCreate{
		BindingID: "b-await-deny", RouteKey: "github.pull_request.opened", WebhookSecret: routerE2ESecret,
	})
	pattern := domain.AwaitEventKey("pull_request", "acme/widgets#7")
	key := awaitE2ESuspendedRun(t, st, "run-guarded", pattern, []string{"release-manager"})

	mux := routerE2EMux(st)
	// The webhook sender is octocat, not an eligible approver.
	resp := routerE2EPost(t, mux, "await-del-deny", routerE2EPRBody("opened", "sha-deny"))
	if resp.Status != "accepted" {
		t.Fatalf("webhook response = %+v, want accepted", resp)
	}

	ctx := context.Background()
	run, err := st.DriverRuns().Get(ctx, routerE2EWS, "run-guarded")
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if run.Status != domain.DriverRunSuspendedAwaitingEvent || run.ResumeSourceEventID != "" {
		t.Fatalf("run = %s/%q, want still suspended with no resume source", run.Status, run.ResumeSourceEventID)
	}
	if _, err := st.Awaits().GetSatisfiedAwait(ctx, routerE2EWS, key); err == nil {
		t.Fatal("GetSatisfiedAwait succeeded after actor rejection, want not-found")
	}
	pending, err := st.Awaits().ListAwaitsByPattern(ctx, routerE2EWS, pattern)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending awaits = %+v, %v; want the guarded await still pending", pending, err)
	}
}
