package webhooks

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type recordingAutomationQueries struct {
	eventWorkspace    string
	eventID           string
	eventFilter       automation.EventFilter
	deliveryWorkspace string
	deliveryID        string
	deliveryFilter    automation.DeliveryFilter
}

func (queries *recordingAutomationQueries) GetEvent(_ context.Context, workspace, eventID string) (*automation.Event, error) {
	queries.eventWorkspace, queries.eventID = workspace, eventID
	return &automation.Event{WorkspaceKey: workspace, EventID: eventID}, nil
}

func (queries *recordingAutomationQueries) ListEvents(_ context.Context, workspace string, filter automation.EventFilter) ([]*automation.Event, error) {
	queries.eventWorkspace, queries.eventFilter = workspace, filter
	return []*automation.Event{{WorkspaceKey: workspace, EventID: "event-1"}}, nil
}

func (queries *recordingAutomationQueries) GetDelivery(_ context.Context, workspace, deliveryID string) (*automation.Delivery, error) {
	queries.deliveryWorkspace, queries.deliveryID = workspace, deliveryID
	return &automation.Delivery{WorkspaceKey: workspace, DeliveryID: deliveryID}, nil
}

func (queries *recordingAutomationQueries) ListDeliveries(_ context.Context, workspace string, filter automation.DeliveryFilter) ([]*automation.Delivery, error) {
	queries.deliveryWorkspace, queries.deliveryFilter = workspace, filter
	return []*automation.Delivery{{WorkspaceKey: workspace, DeliveryID: "delivery-1"}}, nil
}

func TestVerificationDenialsAreUniformAndDoNotAdmit(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		request func() *http.Request
	}{
		{
			name: "signature mismatch", enabled: true,
			request: func() *http.Request {
				request := signedRequest("github", "denied-bad-signature", prOpenedBody)
				request.Header.Set(githubSignatureHeader, "sha256=deadbeef")
				return request
			},
		},
		{
			name: "disabled route", enabled: false,
			request: func() *http.Request {
				return signedRequest("github", "denied-disabled", prOpenedBody)
			},
		},
		{
			name: "missing route", enabled: true,
			request: func() *http.Request {
				body := []byte(`{"action":"closed","repository":{"full_name":"acme/widgets"}}`)
				request := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+testWS+"/webhooks/github", bytes.NewReader(body))
				request.Header.Set(githubEventHeader, "pull_request")
				request.Header.Set(githubDeliveryHeader, "denied-missing")
				request.Header.Set(githubSignatureHeader, githubSignature(testSecret, body))
				return request
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st := seedStore(t, test.enabled)
			recorder := httptest.NewRecorder()
			newServer(st).ServeHTTP(recorder, test.request())
			if recorder.Code != http.StatusUnauthorized || recorder.Body.String() != uniform401Body {
				t.Fatalf("denial = %d %q, want uniform 401", recorder.Code, recorder.Body.String())
			}
			events, err := st.TriggerEvents().List(t.Context(), testWS, store.TriggerEventFilter{})
			if err != nil || len(events) != 0 {
				t.Fatalf("denied request events = %d, %v; want none", len(events), err)
			}
		})
	}
}

func TestReadRoutesUseAutomationQueryAPIsWithLegacyParameters(t *testing.T) {
	queries := &recordingAutomationQueries{}
	mux := http.NewServeMux()
	New(Config{Automation: queries}).Register(mux)

	assertOK := func(path string) {
		t.Helper()
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, body %s", path, recorder.Code, recorder.Body.String())
		}
	}

	assertOK("/api/workspaces/WS/trigger-events?source_kind=github&trigger_binding_id=binding-1&limit=7")
	if queries.eventWorkspace != "WS" || queries.eventFilter != (automation.EventFilter{
		SourceKind: "github", BindingID: "binding-1", Limit: 7,
	}) {
		t.Fatalf("event query = %q %+v", queries.eventWorkspace, queries.eventFilter)
	}
	assertOK("/api/workspaces/WS/trigger-events/event-42")
	if queries.eventWorkspace != "WS" || queries.eventID != "event-42" {
		t.Fatalf("event get = %q/%q", queries.eventWorkspace, queries.eventID)
	}

	assertOK("/api/workspaces/WS/trigger-deliveries?trigger_event_id=event-42&trigger_binding_id=binding-1&status=held&limit=9")
	if queries.deliveryWorkspace != "WS" || queries.deliveryFilter != (automation.DeliveryFilter{
		EventID: "event-42", BindingID: "binding-1", Status: automation.DeliveryHeld, Limit: 9,
	}) {
		t.Fatalf("delivery query = %q %+v", queries.deliveryWorkspace, queries.deliveryFilter)
	}
	assertOK("/api/workspaces/WS/trigger-deliveries/delivery-42")
	if queries.deliveryWorkspace != "WS" || queries.deliveryID != "delivery-42" {
		t.Fatalf("delivery get = %q/%q", queries.deliveryWorkspace, queries.deliveryID)
	}
}

func TestProductionWebhookHandlerHasNoLegacyMutationOrQueryFallback(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current package directory")
	}
	directory := filepath.Dir(currentFile)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}

	forbidden := map[string]struct{}{
		"TriggerRoutes": {}, "DispatchTriggerRoute": {}, "DispatchTriggerRouteV2": {},
		"TriggerEvents": {}, "TriggerDeliveries": {},
	}
	files := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(files, filepath.Join(directory, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if _, blocked := forbidden[selector.Sel.Name]; blocked {
				t.Errorf("production webhook handler %s uses forbidden legacy Store selector %s", name, selector.Sel.Name)
			}
			return true
		})
	}
}
