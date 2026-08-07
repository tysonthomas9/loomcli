package providers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const (
	testDatadogAPIKey = "ddapiSECRETkey000111"
	testDatadogAppKey = "ddappSECRETkey222333"
)

// datadogTestNow is the injected clock for freshness-window tests.
var datadogTestNow = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

// The route-table fake from github_test.go is API-agnostic; the Datadog
// tests reuse it as a fake Datadog API.
func datadogProvider(f *fakeGitHub) *Datadog {
	d := NewDatadog(f.server.Client(), f.server.URL)
	d.now = func() time.Time { return datadogTestNow }
	return d
}

func incidentSpec() CallSpec {
	return CallSpec{
		Action:   ActionDatadogIncidentsWrite,
		Resource: "monitor:42",
		Args: map[string]any{
			"monitorId":        42,
			"title":            "API latency incident",
			"customerImpacted": true,
		},
		IdempotencyKey: "run-1#datadog.incidents.write#0",
		Credential:     testDatadogAPIKey + ":" + testDatadogAppKey,
	}
}

func firingMonitorBody(state, modified string) string {
	return `{"id":42,"name":"api latency","type":"metric alert","overall_state":"` +
		state + `","overall_state_modified":"` + modified + `"}`
}

func assertNoDatadogKeys(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	for _, key := range []string{testDatadogAPIKey, testDatadogAppKey} {
		if strings.Contains(err.Error(), key) {
			t.Fatalf("credential leaked into error text: %v", err)
		}
	}
}

func TestDatadogActionsPassActionGrammar(t *testing.T) {
	for _, action := range DatadogActions() {
		if err := domain.ValidateConnectorAction(action); err != nil {
			t.Errorf("action %q fails the CV1 grammar: %v", action, err)
		}
	}
}

func TestDatadogMonitorsRead(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodGet, "/api/v1/monitor", fakeResponse{
		status: http.StatusOK,
		body:   `[` + firingMonitorBody("Alert", "2026-06-11T11:00:00Z") + `]`,
	})

	result, err := datadogProvider(fake).Call(context.Background(), CallSpec{
		Action:     ActionDatadogMonitorsRead,
		Resource:   "monitor:*",
		Args:       map[string]any{"monitorTags": "service:api", "name": "latency"},
		Credential: testDatadogAPIKey + ":" + testDatadogAppKey,
	})
	if err != nil {
		t.Fatalf("monitors.read: %v", err)
	}
	if result.Decision != domain.ConnectorCallGranted {
		t.Errorf("decision = %q, want granted", result.Decision)
	}
	monitors, ok := result.Body["monitors"].([]map[string]any)
	if !ok || len(monitors) != 1 {
		t.Fatalf("monitors = %+v, want one whitelisted monitor", result.Body["monitors"])
	}
	m := monitors[0]
	if m["overallState"] != "Alert" || m["name"] != "api latency" {
		t.Errorf("monitor = %+v, want camelCase overallState", m)
	}

	req := fake.recorded()[0]
	for _, want := range []string{"monitor_tags=service%3Aapi", "name=latency"} {
		if !strings.Contains(req.Query, want) {
			t.Errorf("query %q missing %q", req.Query, want)
		}
	}
	if got := req.Header.Get("DD-API-KEY"); got != testDatadogAPIKey {
		t.Errorf("DD-API-KEY = %q, want split api key", got)
	}
	if got := req.Header.Get("DD-APPLICATION-KEY"); got != testDatadogAppKey {
		t.Errorf("DD-APPLICATION-KEY = %q, want split app key", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty (Datadog auth is header keys)", got)
	}
}

func TestDatadogAlertRead(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodGet, "/api/v1/monitor/42", fakeResponse{
		status: http.StatusOK,
		body:   firingMonitorBody("Warn", "2026-06-11T11:30:00Z"),
	})

	result, err := datadogProvider(fake).Call(context.Background(), CallSpec{
		Action:     ActionDatadogAlertRead,
		Resource:   "monitor:42",
		Args:       map[string]any{"monitorId": float64(42)},
		Credential: testDatadogAPIKey,
	})
	if err != nil {
		t.Fatalf("alert.read: %v", err)
	}
	if result.Body["overallState"] != "Warn" || result.Body["overallStateModified"] != "2026-06-11T11:30:00Z" {
		t.Errorf("body = %+v, want camelCase alert state fields", result.Body)
	}
	if result.Decision != domain.ConnectorCallGranted {
		t.Errorf("decision = %q, want granted", result.Decision)
	}
}

func TestDatadogIncidentsWriteHappyPath(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodGet, "/api/v1/monitor/42", fakeResponse{
		status: http.StatusOK,
		body:   firingMonitorBody("Alert", "2026-06-11T11:55:00Z"),
	})
	fake.route(http.MethodPost, "/api/v2/incidents", fakeResponse{
		status: http.StatusCreated,
		body:   `{"data":{"id":"inc-abc","type":"incidents","attributes":{"title":"API latency incident"}}}`,
	})

	spec := incidentSpec()
	result, err := datadogProvider(fake).Call(context.Background(), spec)
	if err != nil {
		t.Fatalf("incidents.write: %v", err)
	}
	if result.Status != http.StatusCreated || result.Decision != domain.ConnectorCallGranted {
		t.Fatalf("result = %+v, want status 201 granted", result)
	}
	if result.Body["id"] != "inc-abc" || result.Body["title"] != "API latency incident" {
		t.Errorf("body = %+v", result.Body)
	}

	reqs := fake.recorded()
	if len(reqs) != 2 || reqs[0].Method != http.MethodGet || reqs[1].Method != http.MethodPost {
		t.Fatalf("requests = %+v, want freshness GET then POST", reqs)
	}
	post := reqs[1]
	data, _ := post.Body["data"].(map[string]any)
	attrs, _ := data["attributes"].(map[string]any)
	if attrs["title"] != "API latency incident" || attrs["customer_impacted"] != true {
		t.Errorf("incident payload = %+v", post.Body)
	}
	for _, req := range reqs {
		if got := req.Header.Get("Idempotency-Key"); got != spec.IdempotencyKey {
			t.Errorf("%s %s Idempotency-Key = %q, want runID-derived key", req.Method, req.Path, got)
		}
		if got := req.Header.Get("DD-API-KEY"); got != testDatadogAPIKey {
			t.Errorf("%s %s DD-API-KEY = %q", req.Method, req.Path, got)
		}
	}
}

func TestDatadogIncidentsWriteFreshnessCheck(t *testing.T) {
	tests := []struct {
		name        string
		monitorResp fakeResponse
		wantStale   bool
		staleReason string
	}{
		{
			name: "resolved long ago refuses without issuing the write",
			monitorResp: fakeResponse{
				status: http.StatusOK,
				body:   firingMonitorBody("OK", "2026-06-11T10:00:00Z"),
			},
			wantStale:   true,
			staleReason: "no longer firing",
		},
		{
			name: "monitor gone refuses without issuing the write",
			monitorResp: fakeResponse{
				status: http.StatusNotFound,
				body:   `{"errors":["Monitor not found"]}`,
			},
			wantStale:   true,
			staleReason: "not found",
		},
		{
			name: "recently resolved still proceeds",
			monitorResp: fakeResponse{
				status: http.StatusOK,
				body:   firingMonitorBody("OK", "2026-06-11T11:58:00Z"),
			},
		},
		{
			name: "no data counts as firing",
			monitorResp: fakeResponse{
				status: http.StatusOK,
				body:   firingMonitorBody("No Data", "2026-06-11T09:00:00Z"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			fake.route(http.MethodGet, "/api/v1/monitor/42", tt.monitorResp)
			fake.route(http.MethodPost, "/api/v2/incidents", fakeResponse{
				status: http.StatusCreated,
				body:   `{"data":{"id":"inc-abc","attributes":{"title":"API latency incident"}}}`,
			})

			result, err := datadogProvider(fake).Call(context.Background(), incidentSpec())
			if tt.wantStale {
				var stale *StaleSubject
				if !errors.As(err, &stale) {
					t.Fatalf("error %T is not *StaleSubject (err=%v)", err, err)
				}
				if !strings.Contains(stale.Reason, tt.staleReason) {
					t.Errorf("reason = %q, want substring %q", stale.Reason, tt.staleReason)
				}
				if !errors.Is(err, domain.ErrConflict) {
					t.Error("StaleSubject must match domain.ErrConflict")
				}
				if result.Decision != domain.ConnectorCallStaleSubject {
					t.Errorf("decision = %q, want stale_subject", result.Decision)
				}
				assertNoDatadogKeys(t, err)
				for _, req := range fake.recorded() {
					if req.Method == http.MethodPost {
						t.Fatalf("write was issued despite stale alert: %s %s", req.Method, req.Path)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("incidents.write: %v", err)
			}
			if result.Decision != domain.ConnectorCallGranted {
				t.Errorf("decision = %q, want granted", result.Decision)
			}
		})
	}
}

func TestDatadogIncidentsWriteMissingMonitorID(t *testing.T) {
	fake := newFakeGitHub(t)
	spec := incidentSpec()
	delete(spec.Args, "monitorId")

	result, err := datadogProvider(fake).Call(context.Background(), spec)
	var pre *PreconditionRequired
	if !errors.As(err, &pre) {
		t.Fatalf("error %T is not *PreconditionRequired", err)
	}
	if len(pre.Fields) != 1 || pre.Fields[0] != "monitorId" {
		t.Errorf("fields = %v, want [monitorId]", pre.Fields)
	}
	if !errors.Is(err, domain.ErrInvalid) {
		t.Error("PreconditionRequired must match domain.ErrInvalid")
	}
	if result.Decision != domain.ConnectorCallPreconditionRequired {
		t.Errorf("decision = %q, want precondition_required", result.Decision)
	}
	if DecisionForError(err) != domain.ConnectorCallPreconditionRequired {
		t.Error("DecisionForError must classify PreconditionRequired")
	}
	if n := len(fake.recorded()); n != 0 {
		t.Errorf("fake saw %d requests, want 0 (refused before egress)", n)
	}
}

func TestDatadogIncidentsWriteRequiresIdempotencyKey(t *testing.T) {
	fake := newFakeGitHub(t)
	spec := incidentSpec()
	spec.IdempotencyKey = ""

	_, err := datadogProvider(fake).Call(context.Background(), spec)
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("err = %v, want domain.ErrInvalid", err)
	}
	if n := len(fake.recorded()); n != 0 {
		t.Errorf("fake saw %d requests, want 0", n)
	}
}

func TestDatadogRateLimit(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodGet, "/api/v1/monitor/42", fakeResponse{
		status: http.StatusOK,
		body:   firingMonitorBody("Alert", "2026-06-11T11:55:00Z"),
	})
	fake.route(http.MethodPost, "/api/v2/incidents", fakeResponse{
		status: http.StatusTooManyRequests,
		header: map[string]string{"X-RateLimit-Reset": "25"},
		body:   `{"errors":["rate limit exceeded"]}`,
	})

	result, err := datadogProvider(fake).Call(context.Background(), incidentSpec())
	var rl *RateLimited
	if !errors.As(err, &rl) {
		t.Fatalf("error %T is not *RateLimited (err=%v)", err, err)
	}
	if rl.RetryAfter != 25*time.Second {
		t.Errorf("RetryAfter = %v, want 25s from X-RateLimit-Reset", rl.RetryAfter)
	}
	if !Retryable(err) || !errors.Is(err, ErrUpstream) {
		t.Error("rate limits must be retryable and match ErrUpstream")
	}
	if result.Decision != domain.ConnectorCallUpstreamError {
		t.Errorf("decision = %q, want upstream_error", result.Decision)
	}
}

func TestDatadogUpstreamErrorRedactsKeys(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodGet, "/api/v1/monitor/42", fakeResponse{
		status: http.StatusOK,
		body:   firingMonitorBody("Alert", "2026-06-11T11:55:00Z"),
	})
	fake.route(http.MethodPost, "/api/v2/incidents", fakeResponse{
		status: http.StatusInternalServerError,
		body: `{"errors":["internal error with keys ` +
			testDatadogAPIKey + ` and ` + testDatadogAppKey + `"]}`,
	})

	result, err := datadogProvider(fake).Call(context.Background(), incidentSpec())
	var ue *UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("error %T is not *UpstreamError", err)
	}
	if ue.Class != ClassServerError || !Retryable(err) {
		t.Errorf("class = %q retryable = %v, want retryable server_error", ue.Class, Retryable(err))
	}
	if !strings.Contains(ue.Summary, "[redacted]") {
		t.Errorf("summary = %q, want redaction markers", ue.Summary)
	}
	if result.Status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", result.Status)
	}
	assertNoDatadogKeys(t, err)
}

func TestDatadogNetworkErrorIsRetryableAndRedacted(t *testing.T) {
	fake := newFakeGitHub(t)
	provider := datadogProvider(fake)
	fake.server.Close() // force a transport failure

	_, err := provider.Call(context.Background(), incidentSpec())
	var ue *UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("error %T is not *UpstreamError", err)
	}
	if ue.Class != ClassNetwork || !Retryable(err) {
		t.Errorf("class = %q retryable = %v, want retryable network", ue.Class, Retryable(err))
	}
	assertNoDatadogKeys(t, err)
}

func TestDatadogUnknownActionAndBadArgs(t *testing.T) {
	fake := newFakeGitHub(t)
	provider := datadogProvider(fake)

	_, err := provider.Call(context.Background(), CallSpec{Action: "datadog.monitor.delete"})
	if !errors.Is(err, ErrUnknownAction) || !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("unknown action err = %v, want ErrUnknownAction wrapping domain.ErrInvalid", err)
	}

	_, err = provider.Call(context.Background(), CallSpec{
		Action: ActionDatadogAlertRead,
		Args:   map[string]any{"monitorId": "forty-two"},
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("bad monitorId err = %v, want domain.ErrInvalid", err)
	}

	_, err = provider.Call(context.Background(), CallSpec{Action: ActionDatadogAlertRead})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("missing monitorId err = %v, want domain.ErrInvalid", err)
	}

	if n := len(fake.recorded()); n != 0 {
		t.Errorf("fake saw %d requests, want 0", n)
	}
}
