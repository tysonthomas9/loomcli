package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

const (
	triggerManagementTestWorkspace = "TEST"
)

type triggerManagementFixture struct {
	server         *httptest.Server
	expectedBearer string

	mu             sync.Mutex
	paths          []string
	queries        []string
	authorizations []string
	configRequests int
	createConflict bool
	createRequest  triggerBindingCreateRequest
	patchRequest   triggerBindingPatchRequest
	binding        *automation.Binding
	event          *automation.Event
	delivery       *automation.Delivery
	run            *execution.DriverRunRecord
}

func setupTriggerManagementFixture(t *testing.T) *triggerManagementFixture {
	t.Helper()
	fixture := &triggerManagementFixture{
		expectedBearer: "",
		binding: &automation.Binding{
			WorkspaceKey: triggerManagementTestWorkspace, BindingID: "binding-pr", Name: "PR",
			SourceKind: "github", RouteKey: "github.pull_request.opened", DriverID: "reviewer",
			DriverVersionID: "version-1", ConcurrencyPolicy: automation.ConcurrencyQueue,
			RetryMaxAttempts: 7, RetryBackoffSeconds: 45, Enabled: true,
		},
		event: &automation.Event{
			WorkspaceKey: triggerManagementTestWorkspace, EventID: "event-1", SourceKind: "github",
			EventType: "github.pull_request.opened", SubjectRef: "repo#1", SignatureStatus: "verified",
		},
		delivery: &automation.Delivery{
			WorkspaceKey: triggerManagementTestWorkspace, DeliveryID: "delivery-1", TriggerEventID: "event-1",
			TriggerBindingID: "binding-pr", Status: automation.DeliveryDispatched, DriverRunID: "run-1",
		},
		run: &execution.DriverRunRecord{
			WorkspaceKey: triggerManagementTestWorkspace, RunID: "run-1", DriverID: "reviewer",
			DriverVersionID: "version-1", TriggerBindingID: "binding-pr", Status: execution.DriverRunQueued,
		},
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	configureTriggerManagementClient(t, fixture.server.URL, triggerManagementTestWorkspace)
	return fixture
}

func configureTriggerManagementClient(t *testing.T, serverURL, workspace string) {
	t.Helper()
	t.Setenv("LOOM_SERVER_URL", serverURL)
	t.Setenv("LOOM_WORKSPACE", workspace)
}

func (f *triggerManagementFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/config" {
		f.mu.Lock()
		f.configRequests++
		f.mu.Unlock()
		writeTriggerManagementJSON(w, http.StatusOK, map[string]string{"mode": "open"})
		return
	}

	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	f.queries = append(f.queries, r.URL.RawQuery)
	f.authorizations = append(f.authorizations, r.Header.Get("Authorization"))
	f.mu.Unlock()
	if r.Header.Get("Authorization") != f.expectedBearer {
		writeTriggerManagementJSON(w, http.StatusUnauthorized, map[string]string{"error": "operator credential required", "code": "unauthenticated"})
		return
	}
	prefix := "/api/workspaces/" + triggerManagementTestWorkspace
	if !strings.HasPrefix(r.URL.Path, prefix+"/") {
		writeTriggerManagementJSON(w, http.StatusForbidden, map[string]string{"error": "workspace authority mismatch", "code": "wrong_workspace"})
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == prefix+"/trigger-bindings":
		writeTriggerManagementJSON(w, http.StatusOK, triggerBindingsAPIResponse{Bindings: []*automation.Binding{f.binding}})
	case r.Method == http.MethodPost && r.URL.Path == prefix+"/trigger-bindings":
		f.mu.Lock()
		createConflict := f.createConflict
		f.mu.Unlock()
		if createConflict {
			writeTriggerManagementJSON(w, http.StatusConflict, map[string]string{"error": "trigger binding already exists", "code": "already_exists"})
			return
		}
		var request triggerBindingCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeTriggerManagementJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON", "code": "invalid"})
			return
		}
		f.mu.Lock()
		f.createRequest = request
		f.mu.Unlock()
		created := *f.binding
		created.BindingID = request.BindingID
		created.Name = request.Name
		created.RouteKey = request.RouteKey
		created.EventTypePatterns = append([]string(nil), request.EventTypePatterns...)
		created.SubjectKeyTemplate = request.SubjectKeyTemplate
		created.ConcurrencyPolicy = request.ConcurrencyPolicy
		created.ActorFilter = request.ActorFilter
		created.RetryMaxAttempts = request.RetryMaxAttempts
		created.RetryBackoffSeconds = request.RetryBackoffSeconds
		created.Schedule = request.Schedule
		created.ScheduleTimezone = request.ScheduleTimezone
		writeTriggerManagementJSON(w, http.StatusCreated, &created)
	case r.Method == http.MethodPatch && r.URL.Path == prefix+"/trigger-bindings/binding-pr":
		var request triggerBindingPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeTriggerManagementJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON", "code": "invalid"})
			return
		}
		f.mu.Lock()
		f.patchRequest = request
		f.mu.Unlock()
		writeTriggerManagementJSON(w, http.StatusOK, f.binding)
	case r.Method == http.MethodDelete && r.URL.Path == prefix+"/trigger-bindings/binding-pr":
		writeTriggerManagementJSON(w, http.StatusOK, deleteBindingAPIResponse{BindingID: "binding-pr", Deleted: true, GrantsRevoked: 2})
	case r.Method == http.MethodPost && r.URL.Path == prefix+"/trigger-bindings/binding-pr/run":
		writeTriggerManagementJSON(w, http.StatusAccepted, f.run)
	case r.Method == http.MethodGet && r.URL.Path == prefix+"/trigger-events":
		writeTriggerManagementJSON(w, http.StatusOK, triggerEventsAPIResponse{Events: []*automation.Event{f.event}, Count: 1})
	case r.Method == http.MethodGet && r.URL.Path == prefix+"/trigger-events/event-1":
		writeTriggerManagementJSON(w, http.StatusOK, f.event)
	case r.Method == http.MethodGet && r.URL.Path == prefix+"/trigger-deliveries":
		writeTriggerManagementJSON(w, http.StatusOK, triggerDeliveriesAPIResponse{Deliveries: []*automation.Delivery{f.delivery}, Count: 1})
	case r.Method == http.MethodGet && r.URL.Path == prefix+"/trigger-deliveries/delivery-1":
		writeTriggerManagementJSON(w, http.StatusOK, f.delivery)
	default:
		writeTriggerManagementJSON(w, http.StatusNotFound, map[string]string{"error": "route not found", "code": "not_found"})
	}
}

func writeTriggerManagementJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (f *triggerManagementFixture) snapshot() (paths, queries, authorizations []string, configRequests int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...), append([]string(nil), f.queries...), append([]string(nil), f.authorizations...), f.configRequests
}

func (f *triggerManagementFixture) requestSnapshot() (triggerBindingCreateRequest, triggerBindingPatchRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createRequest, f.patchRequest
}

func TestTriggerManagementRequiresExplicitEndpointAndWorkspace(t *testing.T) {
	t.Run("endpoint", func(t *testing.T) {
		t.Setenv("LOOM_SERVER_URL", "")
		t.Setenv("LOOM_WORKSPACE", triggerManagementTestWorkspace)
		_, err := newTriggerManagementClient(context.Background())
		if err == nil || !strings.Contains(err.Error(), "require --server or LOOM_SERVER_URL") {
			t.Fatalf("newTriggerManagementClient error = %v, want explicit endpoint requirement", err)
		}
	})

	t.Run("workspace", func(t *testing.T) {
		fixture := setupTriggerManagementFixture(t)
		t.Setenv("LOOM_WORKSPACE", "")
		_, err := newTriggerManagementClient(context.Background())
		if err == nil || !strings.Contains(err.Error(), "require --workspace or LOOM_WORKSPACE") {
			t.Fatalf("newTriggerManagementClient error = %v, want explicit workspace requirement", err)
		}
		paths, _, _, _ := fixture.snapshot()
		if len(paths) != 0 {
			t.Fatalf("management paths = %v, want none without a workspace", paths)
		}
	})
}

func TestTriggerManagementUnavailableHostFailsClosed(t *testing.T) {
	fixture := setupTriggerManagementFixture(t)
	serverURL := fixture.server.URL
	fixture.server.Close()
	t.Setenv("LOOM_SERVER_URL", serverURL)

	_, err := newTriggerManagementClient(context.Background())
	if err == nil || !strings.Contains(err.Error(), "endpoint discovery") || !strings.Contains(err.Error(), serverURL) {
		t.Fatalf("newTriggerManagementClient error = %v, want unavailable configured endpoint", err)
	}
}

func TestTriggerManagementDuplicateCreatePreservesConflictExitClass(t *testing.T) {
	fixture := setupTriggerManagementFixture(t)
	fixture.mu.Lock()
	fixture.createConflict = true
	fixture.mu.Unlock()
	client, err := newTriggerManagementClient(context.Background())
	if err != nil {
		t.Fatalf("newTriggerManagementClient: %v", err)
	}
	enabled := true
	_, err = client.createBinding(context.Background(), triggerBindingCreateRequest{
		BindingID: "binding-pr", Name: "PR", SourceKind: "github", RouteKey: "github.pull_request.opened",
		DriverID: "reviewer", DriverVersionID: "version-1", Enabled: &enabled,
	})
	if !errors.Is(err, persistence.ErrConflict) || !strings.Contains(err.Error(), "code=already_exists") {
		t.Fatalf("duplicate create error = %v, want conflict exit class", err)
	}
	_, queries, _, _ := fixture.snapshot()
	if !containsString(queries, "create_only=true") {
		t.Fatalf("management queries = %v, want strict create_only contract", queries)
	}
}

func TestTriggerManagementOpenModeRequiresNoLocalCredential(t *testing.T) {
	fixture := setupTriggerManagementFixture(t)
	client, err := newTriggerManagementClient(context.Background())
	if err != nil {
		t.Fatalf("newTriggerManagementClient without local credential: %v", err)
	}
	if _, err := client.listBindings(context.Background()); err != nil {
		t.Fatalf("listBindings without local credential: %v", err)
	}
	_, _, authorizations, _ := fixture.snapshot()
	for _, got := range authorizations {
		if got != "" {
			t.Fatalf("Authorization = %q, want none in open mode", got)
		}
	}
}

func TestTriggerManagementClientUsesNoOpenCredentialAndPreservesAllRoutesAndFields(t *testing.T) {
	fixture := setupTriggerManagementFixture(t)
	ctx := context.Background()
	client, err := newTriggerManagementClient(ctx)
	if err != nil {
		t.Fatalf("newTriggerManagementClient: %v", err)
	}
	enabled := true
	created, err := client.createBinding(ctx, triggerBindingCreateRequest{
		DriverID: "reviewer", DriverVersionID: "version-1", BindingID: "binding-created", Name: "Created",
		SourceKind: "github", RouteKey: "github.pull_request.opened", Enabled: &enabled,
		EventTypePatterns: []string{"github.pull_request.*"}, SubjectKeyTemplate: "{{subject_ref}}",
		ConcurrencyPolicy: automation.ConcurrencyQueue,
		ActorFilter:       &automation.ActorFilter{ExcludeActorKinds: []string{"workflow"}},
		RetryMaxAttempts:  9, RetryBackoffSeconds: 60, Schedule: "@hourly", ScheduleTimezone: "UTC",
	})
	if err != nil || created.BindingID != "binding-created" {
		t.Fatalf("createBinding = %+v err=%v", created, err)
	}
	patterns := []string{"github.{push,release}.*"}
	policy := automation.ConcurrencyReplace
	retryMax, retryBackoff := 3, 90
	subjectTemplate := "{{event_type}}/{{subject_ref}}"
	if _, err := client.updateBinding(ctx, "binding-pr", triggerBindingPatchRequest{
		EventTypePatterns: &patterns, SubjectKeyTemplate: &subjectTemplate, ConcurrencyPolicy: &policy,
		ClearActorFilter: true, RetryMaxAttempts: &retryMax, RetryBackoffSeconds: &retryBackoff,
	}); err != nil {
		t.Fatalf("updateBinding: %v", err)
	}
	if binding, err := client.getBinding(ctx, "binding-pr"); err != nil || binding.BindingID != "binding-pr" {
		t.Fatalf("getBinding = %+v err=%v", binding, err)
	}
	if result, err := client.deleteBinding(ctx, "binding-pr"); err != nil || result.GrantsRevoked != 2 {
		t.Fatalf("deleteBinding = %+v err=%v", result, err)
	}
	if run, err := client.runBinding(ctx, "binding-pr"); err != nil || run.RunID != "run-1" {
		t.Fatalf("runBinding = %+v err=%v", run, err)
	}
	if events, err := client.listEvents(ctx, "github", 7); err != nil || len(events) != 1 {
		t.Fatalf("listEvents = %+v err=%v", events, err)
	}
	if event, err := client.getEvent(ctx, "event-1"); err != nil || event.EventID != "event-1" {
		t.Fatalf("getEvent = %+v err=%v", event, err)
	}
	if deliveries, err := client.listDeliveries(ctx, "event-1", "dispatched", 9); err != nil || len(deliveries) != 1 {
		t.Fatalf("listDeliveries = %+v err=%v", deliveries, err)
	}
	if delivery, err := client.getDelivery(ctx, "delivery-1"); err != nil || delivery.DeliveryID != "delivery-1" {
		t.Fatalf("getDelivery = %+v err=%v", delivery, err)
	}

	createRequest, patchRequest := fixture.requestSnapshot()
	if got := createRequest; got.SubjectKeyTemplate != "{{subject_ref}}" || got.ConcurrencyPolicy != automation.ConcurrencyQueue ||
		got.ActorFilter == nil || got.RetryMaxAttempts != 9 || got.RetryBackoffSeconds != 60 || len(got.EventTypePatterns) != 1 {
		t.Fatalf("create request dropped Router-v2 fields: %+v", got)
	}
	if got := patchRequest; got.SubjectKeyTemplate == nil || got.ConcurrencyPolicy == nil || !got.ClearActorFilter ||
		got.RetryMaxAttempts == nil || got.RetryBackoffSeconds == nil || got.EventTypePatterns == nil {
		t.Fatalf("patch request dropped Router-v2 fields: %+v", got)
	}
	paths, queries, authorizations, configRequests := fixture.snapshot()
	for _, want := range []string{
		"POST /api/workspaces/TEST/trigger-bindings",
		"PATCH /api/workspaces/TEST/trigger-bindings/binding-pr",
		"GET /api/workspaces/TEST/trigger-bindings",
		"DELETE /api/workspaces/TEST/trigger-bindings/binding-pr",
		"POST /api/workspaces/TEST/trigger-bindings/binding-pr/run",
		"GET /api/workspaces/TEST/trigger-events",
		"GET /api/workspaces/TEST/trigger-events/event-1",
		"GET /api/workspaces/TEST/trigger-deliveries",
		"GET /api/workspaces/TEST/trigger-deliveries/delivery-1",
	} {
		if !containsString(paths, want) {
			t.Fatalf("management paths = %v, missing %q", paths, want)
		}
	}
	if !containsString(queries, "limit=7&source_kind=github") || !containsString(queries, "limit=9&status=dispatched&trigger_event_id=event-1") {
		t.Fatalf("management queries = %v, want event and delivery filters", queries)
	}
	for _, got := range authorizations {
		if got != "" {
			t.Fatalf("Authorization = %q, want none in open mode", got)
		}
	}
	if configRequests != 1 {
		t.Fatalf("GET /api/config count = %d, want one discovery per client", configRequests)
	}
}

func TestTriggerManagementCommandJSONCompatibility(t *testing.T) {
	setupTriggerManagementFixture(t)
	restoreTriggerCommandGlobals(t)

	bindCreateRouteKey = "github.pull_request.opened"
	bindCreateDriver = "reviewer"
	bindCreateVersion = "version-1"
	bindCreateBindingID = "binding-created"
	bindCreateName = "Created"
	bindCreateSource = "github"
	bindCreateJSON = true
	createdJSON, err := captureTriggerStdout(t, func() error { return runBindingsCreate(&cobra.Command{}, nil) })
	if err != nil {
		t.Fatalf("runBindingsCreate: %v", err)
	}
	var created automation.Binding
	if err := json.Unmarshal([]byte(createdJSON), &created); err != nil || created.BindingID != "binding-created" {
		t.Fatalf("create JSON = %q decoded=%+v err=%v", createdJSON, created, err)
	}

	bindUpdateJSON = true
	updateCmd := &cobra.Command{Use: "update"}
	registerRouterBindingFlags(updateCmd, &bindUpdateRouter)
	if err := updateCmd.Flags().Parse([]string{"--concurrency-policy", "replace"}); err != nil {
		t.Fatalf("parse update flags: %v", err)
	}
	updatedJSON, err := captureTriggerStdout(t, func() error { return runBindingsUpdate(updateCmd, []string{"binding-pr"}) })
	if err != nil {
		t.Fatalf("runBindingsUpdate: %v", err)
	}
	var updated automation.Binding
	if err := json.Unmarshal([]byte(updatedJSON), &updated); err != nil || updated.BindingID != "binding-pr" {
		t.Fatalf("update JSON = %q decoded=%+v err=%v", updatedJSON, updated, err)
	}

	bindListJSON = true
	listedJSON, err := captureTriggerStdout(t, func() error { return runBindingsList(&cobra.Command{}, nil) })
	if err != nil {
		t.Fatalf("runBindingsList: %v", err)
	}
	var listed []*automation.Binding
	if err := json.Unmarshal([]byte(listedJSON), &listed); err != nil || len(listed) != 1 || listed[0].BindingID != "binding-pr" {
		t.Fatalf("list JSON = %q decoded=%+v err=%v", listedJSON, listed, err)
	}

	shownJSON, err := captureTriggerStdout(t, func() error { return runBindingsShow(&cobra.Command{}, []string{"binding-pr"}) })
	if err != nil {
		t.Fatalf("runBindingsShow: %v", err)
	}
	var shown automation.Binding
	if err := json.Unmarshal([]byte(shownJSON), &shown); err != nil || shown.BindingID != "binding-pr" {
		t.Fatalf("show JSON = %q decoded=%+v err=%v", shownJSON, shown, err)
	}

	bindDeleteJSON = true
	deletedJSON, err := captureTriggerStdout(t, func() error { return runBindingsDelete(&cobra.Command{}, []string{"binding-pr"}) })
	if err != nil {
		t.Fatalf("runBindingsDelete: %v", err)
	}
	var deleted deleteBindingAPIResponse
	if err := json.Unmarshal([]byte(deletedJSON), &deleted); err != nil || !deleted.Deleted || deleted.GrantsRevoked != 2 {
		t.Fatalf("delete JSON = %q decoded=%+v err=%v", deletedJSON, deleted, err)
	}

	bindRunJSON = true
	runJSON, err := captureTriggerStdout(t, func() error { return runBindingsRun(&cobra.Command{}, []string{"binding-pr"}) })
	if err != nil {
		t.Fatalf("runBindingsRun: %v", err)
	}
	var run execution.DriverRunRecord
	if err := json.Unmarshal([]byte(runJSON), &run); err != nil || run.RunID != "run-1" {
		t.Fatalf("run JSON = %q decoded=%+v err=%v", runJSON, run, err)
	}

	eventsListJSON = true
	eventsJSON, err := captureTriggerStdout(t, func() error { return runEventsList(&cobra.Command{}, nil) })
	if err != nil {
		t.Fatalf("runEventsList: %v", err)
	}
	var events []*automation.Event
	if err := json.Unmarshal([]byte(eventsJSON), &events); err != nil || len(events) != 1 || events[0].EventID != "event-1" {
		t.Fatalf("events JSON = %q decoded=%+v err=%v", eventsJSON, events, err)
	}

	eventJSON, err := captureTriggerStdout(t, func() error { return runEventsShow(&cobra.Command{}, []string{"event-1"}) })
	if err != nil {
		t.Fatalf("runEventsShow: %v", err)
	}
	var event automation.Event
	if err := json.Unmarshal([]byte(eventJSON), &event); err != nil || event.EventID != "event-1" {
		t.Fatalf("event JSON = %q decoded=%+v err=%v", eventJSON, event, err)
	}

	delivListJSON = true
	deliveriesJSON, err := captureTriggerStdout(t, func() error { return runDeliveriesList(&cobra.Command{}, nil) })
	if err != nil {
		t.Fatalf("runDeliveriesList: %v", err)
	}
	var deliveries []*automation.Delivery
	if err := json.Unmarshal([]byte(deliveriesJSON), &deliveries); err != nil || len(deliveries) != 1 || deliveries[0].DeliveryID != "delivery-1" {
		t.Fatalf("deliveries JSON = %q decoded=%+v err=%v", deliveriesJSON, deliveries, err)
	}

	deliveryJSON, err := captureTriggerStdout(t, func() error { return runDeliveriesShow(&cobra.Command{}, []string{"delivery-1"}) })
	if err != nil {
		t.Fatalf("runDeliveriesShow: %v", err)
	}
	var delivery automation.Delivery
	if err := json.Unmarshal([]byte(deliveryJSON), &delivery); err != nil || delivery.DeliveryID != "delivery-1" {
		t.Fatalf("delivery JSON = %q decoded=%+v err=%v", deliveryJSON, delivery, err)
	}
}

func TestTriggerManagementCommandTextCompatibility(t *testing.T) {
	fixture := setupTriggerManagementFixture(t)
	restoreTriggerCommandGlobals(t)

	bindCreateRouteKey = "github.pull_request.opened"
	bindCreateDriver = "reviewer"
	bindCreateVersion = "version-1"
	bindCreateBindingID = "binding-created"
	bindCreateName = "Created"
	bindCreateSource = "github"
	createCmd := &cobra.Command{}
	var createOut strings.Builder
	createCmd.SetOut(&createOut)
	if err := runBindingsCreate(createCmd, nil); err != nil {
		t.Fatalf("runBindingsCreate: %v", err)
	}
	if want := "Created trigger binding binding-created (route github.pull_request.opened → driver reviewer version version-1, enabled=true)\n"; createOut.String() != want {
		t.Fatalf("create output = %q, want %q", createOut.String(), want)
	}

	updateCmd := &cobra.Command{Use: "update"}
	registerRouterBindingFlags(updateCmd, &bindUpdateRouter)
	if err := updateCmd.Flags().Parse([]string{"--concurrency-policy", "replace"}); err != nil {
		t.Fatalf("parse update flags: %v", err)
	}
	var updateOut strings.Builder
	updateCmd.SetOut(&updateOut)
	if err := runBindingsUpdate(updateCmd, []string{"binding-pr"}); err != nil {
		t.Fatalf("runBindingsUpdate: %v", err)
	}
	if want := "Updated trigger binding binding-pr (policy=queue, enabled=true)\n"; updateOut.String() != want {
		t.Fatalf("update output = %q, want %q", updateOut.String(), want)
	}

	listCmd := &cobra.Command{}
	var listOut strings.Builder
	listCmd.SetOut(&listOut)
	if err := runBindingsList(listCmd, nil); err != nil {
		t.Fatalf("runBindingsList: %v", err)
	}
	if want := formatBindingRow(fixture.binding) + "\n"; listOut.String() != want {
		t.Fatalf("list output = %q, want %q", listOut.String(), want)
	}

	deleteCmd := &cobra.Command{}
	var deleteOut strings.Builder
	deleteCmd.SetOut(&deleteOut)
	if err := runBindingsDelete(deleteCmd, []string{"binding-pr"}); err != nil {
		t.Fatalf("runBindingsDelete: %v", err)
	}
	if want := "Deleted trigger binding binding-pr (grants revoked=2)\n"; deleteOut.String() != want {
		t.Fatalf("delete output = %q, want %q", deleteOut.String(), want)
	}

	eventsCmd := &cobra.Command{}
	var eventsOut strings.Builder
	eventsCmd.SetOut(&eventsOut)
	if err := runEventsList(eventsCmd, nil); err != nil {
		t.Fatalf("runEventsList: %v", err)
	}
	if !strings.Contains(eventsOut.String(), "event-1") || !strings.Contains(eventsOut.String(), "github.pull_request.opened") {
		t.Fatalf("events output = %q, want legacy event columns", eventsOut.String())
	}

	deliveriesCmd := &cobra.Command{}
	var deliveriesOut strings.Builder
	deliveriesCmd.SetOut(&deliveriesOut)
	if err := runDeliveriesList(deliveriesCmd, nil); err != nil {
		t.Fatalf("runDeliveriesList: %v", err)
	}
	if !strings.Contains(deliveriesOut.String(), "delivery-1") || !strings.Contains(deliveriesOut.String(), "run=run-1") {
		t.Fatalf("deliveries output = %q, want legacy delivery columns", deliveriesOut.String())
	}
}

func TestTriggerManagementAuthAndWorkspaceFailuresRemainDistinct(t *testing.T) {
	t.Run("unauthorized", func(t *testing.T) {
		fixture := setupTriggerManagementFixture(t)
		fixture.expectedBearer = "Bearer required"
		configureTriggerManagementClient(t, fixture.server.URL, triggerManagementTestWorkspace)
		client, err := newTriggerManagementClient(context.Background())
		if err != nil {
			t.Fatalf("newTriggerManagementClient: %v", err)
		}
		_, err = client.listBindings(context.Background())
		if err == nil || !strings.Contains(err.Error(), "unauthorized") || !strings.Contains(err.Error(), "code=unauthenticated") {
			t.Fatalf("listBindings error = %v, want unauthenticated class", err)
		}
	})

	t.Run("wrong workspace", func(t *testing.T) {
		fixture := setupTriggerManagementFixture(t)
		configureTriggerManagementClient(t, fixture.server.URL, "OTHER")
		client, err := newTriggerManagementClient(context.Background())
		if err != nil {
			t.Fatalf("newTriggerManagementClient: %v", err)
		}
		_, err = client.listBindings(context.Background())
		if err == nil || !strings.Contains(err.Error(), "forbidden") || !strings.Contains(err.Error(), "code=wrong_workspace") {
			t.Fatalf("listBindings error = %v, want wrong-workspace class", err)
		}
	})
}

func TestTriggerManagementStatusErrorsPreserveDomainExitClasses(t *testing.T) {
	for _, test := range []struct {
		status int
		want   error
	}{
		{status: http.StatusBadRequest, want: persistence.ErrInvalid},
		{status: http.StatusNotFound, want: persistence.ErrNotFound},
		{status: http.StatusConflict, want: persistence.ErrConflict},
	} {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			err := triggerManagementStatusError(test.status, []byte(`{"error":"test failure","code":"test"}`))
			if !strings.Contains(err.Error(), fmt.Sprint(test.status)) || !strings.Contains(err.Error(), "code=test") {
				t.Fatalf("error = %v, want status and code", err)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, test.want)
			}
		})
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func restoreTriggerCommandGlobals(t *testing.T) {
	t.Helper()
	type state struct {
		createRoute, createWorkflow, createDriver, createVersion, createName, createSource, createID, createEntry string
		createDisabled, createJSON                                                                                bool
		createRouter                                                                                              routerBindingFlags
		updateJSON                                                                                                bool
		updateRouter                                                                                              routerBindingFlags
		listSource                                                                                                string
		listEnabled, listJSON, deleteJSON, runJSON                                                                bool
		eventSource                                                                                               string
		eventLimit                                                                                                int
		eventJSON                                                                                                 bool
		deliveryEvent, deliveryStatus                                                                             string
		deliveryLimit                                                                                             int
		deliveryJSON                                                                                              bool
	}
	saved := state{
		createRoute: bindCreateRouteKey, createWorkflow: bindCreateWorkflow, createDriver: bindCreateDriver,
		createVersion: bindCreateVersion, createName: bindCreateName,
		createSource: bindCreateSource, createID: bindCreateBindingID, createEntry: bindCreateEntry,
		createDisabled: bindCreateDisabled, createJSON: bindCreateJSON, createRouter: bindCreateRouter,
		updateJSON: bindUpdateJSON, updateRouter: bindUpdateRouter,
		listSource: bindListSource, listEnabled: bindListEnabled, listJSON: bindListJSON,
		deleteJSON: bindDeleteJSON, runJSON: bindRunJSON,
		eventSource: eventsListSource, eventLimit: eventsListLimit, eventJSON: eventsListJSON,
		deliveryEvent: delivListEvent, deliveryStatus: delivListStatus, deliveryLimit: delivListLimit, deliveryJSON: delivListJSON,
	}
	t.Cleanup(func() {
		bindCreateRouteKey, bindCreateWorkflow, bindCreateDriver, bindCreateVersion = saved.createRoute, saved.createWorkflow, saved.createDriver, saved.createVersion
		bindCreateName, bindCreateSource, bindCreateBindingID, bindCreateEntry = saved.createName, saved.createSource, saved.createID, saved.createEntry
		bindCreateDisabled, bindCreateJSON, bindCreateRouter = saved.createDisabled, saved.createJSON, saved.createRouter
		bindUpdateJSON, bindUpdateRouter = saved.updateJSON, saved.updateRouter
		bindListSource, bindListEnabled, bindListJSON = saved.listSource, saved.listEnabled, saved.listJSON
		bindDeleteJSON, bindRunJSON = saved.deleteJSON, saved.runJSON
		eventsListSource, eventsListLimit, eventsListJSON = saved.eventSource, saved.eventLimit, saved.eventJSON
		delivListEvent, delivListStatus, delivListLimit, delivListJSON = saved.deliveryEvent, saved.deliveryStatus, saved.deliveryLimit, saved.deliveryJSON
	})
	bindCreateRouteKey, bindCreateWorkflow, bindCreateDriver, bindCreateVersion = "", "", "", ""
	bindCreateName, bindCreateSource, bindCreateBindingID, bindCreateEntry = "", "", "", ""
	bindCreateDisabled, bindCreateJSON, bindCreateRouter = false, false, routerBindingFlags{}
	bindUpdateJSON, bindUpdateRouter = false, routerBindingFlags{}
	bindListSource, bindListEnabled, bindListJSON = "", false, false
	bindDeleteJSON, bindRunJSON = false, false
	eventsListSource, eventsListLimit, eventsListJSON = "", 0, false
	delivListEvent, delivListStatus, delivListLimit, delivListJSON = "", "", 0, false
}

func captureTriggerStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = writer
	runErr := fn()
	_ = writer.Close()
	os.Stdout = original
	data, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil && runErr == nil {
		runErr = readErr
	}
	return string(data), runErr
}
