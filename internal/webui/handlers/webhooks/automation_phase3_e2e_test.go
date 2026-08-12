//go:build e2e
// +build e2e

package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	automationPhase3DriverID          = "github-pr-review"
	automationPhase3VersionID         = "github-pr-review-v1"
	automationPhase3BindingID         = "binding-github-pr-opened"
	automationPhase3WebhookSecret     = "e2e-webhook-secret"
	automationPhase3PerformanceSample = 30
	automationPhase3P50BudgetMS       = 100.0
	automationPhase3P95BudgetMS       = 250.0
	automationPhase3RoundTrips        = 12
)

// TestE2E_AutomationPhase3RealFleetDBLoomHTTPAndCLI is the Phase 3 public-path
// acceptance proof. Direct FleetDB access stops after fixture setup and final
// durability reads; binding lifecycle, manual dispatch, webhook ingestion,
// and audit inspection all cross real loom serve through the public CLI/HTTP
// contracts.
func TestE2E_AutomationPhase3RealFleetDBLoomHTTPAndCLI(t *testing.T) {
	e2e := newGitHubWebhookE2E(t)
	e2e.startFleetDB()
	e2e.seedWorkspace()
	e2e.seedAutomationPhase3Target()
	e2e.startLoomServe()

	createBody := e2e.automationPhase3CreateBindingBody()
	var created domain.TriggerBinding
	e2e.doAutomationPhase3LoomJSON(
		http.MethodPost,
		"/api/workspaces/"+e2e.workspace+"/trigger-bindings?create_only=true",
		createBody,
		"",
		http.StatusCreated,
		&created,
	)
	assertAutomationPhase3Binding(t, &created, automationPhase3BindingID, domain.TriggerBindingConcurrencyAllow)
	if created.WebhookSecret != "" {
		t.Fatalf("public create leaked webhook secret: %+v", created)
	}

	var listed []*domain.TriggerBinding
	e2e.runAutomationPhase3CLIJSON(&listed, "trigger", "bindings", "list", "--source-kind", "github", "--json")
	assertAutomationPhase3BindingListed(t, listed, automationPhase3BindingID)

	var shown domain.TriggerBinding
	e2e.runAutomationPhase3CLIJSON(&shown, "trigger", "bindings", "show", automationPhase3BindingID)
	assertAutomationPhase3Binding(t, &shown, automationPhase3BindingID, domain.TriggerBindingConcurrencyAllow)

	var updated domain.TriggerBinding
	e2e.runAutomationPhase3CLIJSON(&updated,
		"trigger", "bindings", "update", automationPhase3BindingID,
		"--concurrency-policy", "allow",
		"--retry-max-attempts", "3",
		"--retry-backoff", "2",
		"--json",
	)
	assertAutomationPhase3Binding(t, &updated, automationPhase3BindingID, domain.TriggerBindingConcurrencyAllow)
	if updated.RetryMaxAttempts != 3 || updated.RetryBackoffSeconds != 2 {
		t.Fatalf("updated retry policy = attempts %d backoff %ds, want 3/2s", updated.RetryMaxAttempts, updated.RetryBackoffSeconds)
	}

	first := e2e.postGitHubWebhook("e2e-delivery-1", http.StatusAccepted)
	firstRunID := first.primaryRunID(t)
	webhookRun := e2e.expectQueuedDriverRun(firstRunID)
	event := e2e.expectTriggerEvent(&created)
	e2e.expectTriggerDelivery(event, &created, webhookRun)
	e2e.expectRunEvents(webhookRun.RunID, "driver_run.create")

	var events []*domain.TriggerEvent
	e2e.runAutomationPhase3CLIJSON(&events, "trigger", "events", "list", "--source-kind", "github", "--json")
	assertAutomationPhase3EventListed(t, events, event.EventID)
	var shownEvent domain.TriggerEvent
	e2e.runAutomationPhase3CLIJSON(&shownEvent, "trigger", "events", "show", event.EventID)
	if shownEvent.EventID != event.EventID || shownEvent.SourceEventID != "e2e-delivery-1" || shownEvent.SignatureStatus != "verified" {
		t.Fatalf("CLI event show = %+v, want verified %s", shownEvent, event.EventID)
	}

	var deliveries []*domain.TriggerDelivery
	e2e.runAutomationPhase3CLIJSON(&deliveries, "trigger", "deliveries", "list", "--event", event.EventID, "--json")
	delivery := assertAutomationPhase3DeliveryListed(t, deliveries, event.EventID, firstRunID)
	var shownDelivery domain.TriggerDelivery
	e2e.runAutomationPhase3CLIJSON(&shownDelivery, "trigger", "deliveries", "show", delivery.DeliveryID)
	if shownDelivery.DeliveryID != delivery.DeliveryID || shownDelivery.DriverRunID != firstRunID {
		t.Fatalf("CLI delivery show = %+v, want delivery %s run %s", shownDelivery, delivery.DeliveryID, firstRunID)
	}

	second := e2e.postGitHubWebhook("e2e-delivery-1", http.StatusAccepted)
	if second.primaryRunID(t) != firstRunID {
		t.Fatalf("redelivery driver_run_id = %q, want original %q", second.primaryRunID(t), firstRunID)
	}
	e2e.expectIdempotentRedelivery(firstRunID)

	// Keep the manual dispatch after the duplicate-delivery assertion so the
	// shared webhook verifier can prove the webhook itself created exactly one
	// run. The manual path is then proven independently through the public CLI.
	var manualRun domain.DriverRun
	e2e.runAutomationPhase3CLIJSON(&manualRun, "trigger", "bindings", "run", automationPhase3BindingID, "--json")
	assertAutomationPhase3Run(t, &manualRun, automationPhase3BindingID)

	var deleted automationPhase3DeleteResult
	e2e.runAutomationPhase3CLIJSON(&deleted, "trigger", "bindings", "delete", automationPhase3BindingID, "--json")
	if !deleted.Deleted || deleted.BindingID != automationPhase3BindingID {
		t.Fatalf("CLI delete = %+v, want deleted %s", deleted, automationPhase3BindingID)
	}
	e2e.assertAutomationPhase3BindingDeleted(automationPhase3BindingID)

}

// TestE2E_AutomationPhase3WebhookPerformance retains 30 real signed webhook
// samples and observes only Loom-to-FleetDB HTTP round trips. Fixture writes
// and assertions use the direct FleetDB client and are outside each snapshot.
func TestE2E_AutomationPhase3WebhookPerformance(t *testing.T) {
	e2e := newGitHubWebhookE2E(t)
	e2e.startFleetDB()
	e2e.seedWorkspace()
	e2e.seedAutomationPhase3Target()

	proxyURL, fleetRequests := startAutomationPhase3CountingProxy(t, e2e.fleetURL)
	e2e.fleetURL = proxyURL
	e2e.startLoomServe()

	var binding domain.TriggerBinding
	e2e.runAutomationPhase3CLIJSON(&binding,
		"trigger", "bindings", "create",
		"--binding-id", automationPhase3BindingID,
		"--source", "github",
		"--route-key", "github.pull_request.opened",
		"--driver", automationPhase3DriverID,
		"--driver-version", automationPhase3VersionID,
		"--entrypoint", "run",
		"--secret", automationPhase3WebhookSecret,
		"--concurrency-policy", "allow",
		"--json",
	)
	assertAutomationPhase3Binding(t, &binding, automationPhase3BindingID, domain.TriggerBindingConcurrencyAllow)

	durationsMS := make([]float64, 0, automationPhase3PerformanceSample)
	for sample := 0; sample < automationPhase3PerformanceSample; sample++ {
		beforeRequests := fleetRequests.Snapshot()
		started := time.Now()
		deliveryID := fmt.Sprintf("phase3-performance-%02d", sample+1)
		response := e2e.postGitHubWebhook(deliveryID, http.StatusAccepted)
		elapsed := time.Since(started)
		if response.primaryRunID(t) == "" {
			t.Fatalf("performance sample %d returned no run", sample+1)
		}
		trace := fleetRequests.Since(beforeRequests)
		if sample == 0 {
			t.Logf("Automation webhook Loom-to-FleetDB request trace: %v", trace)
		}
		if len(trace) != automationPhase3RoundTrips {
			t.Fatalf("Automation webhook sample %d round trips = %d, want pinned %d; trace=%v", sample+1, len(trace), automationPhase3RoundTrips, trace)
		}
		durationsMS = append(durationsMS, float64(elapsed)/float64(time.Millisecond))
	}
	p50 := automationPhase3NearestRankMS(durationsMS, 0.50)
	p95 := automationPhase3NearestRankMS(durationsMS, 0.95)
	t.Logf("Automation webhook raw durations (ms, n=%d): [%s]", len(durationsMS), automationPhase3FormatSamplesMS(durationsMS))
	t.Logf("Automation webhook nearest-rank latency: p50=%.3fms p95=%.3fms; Loom-to-FleetDB round trips=%d per sample", p50, p95, automationPhase3RoundTrips)
	if p50 > automationPhase3P50BudgetMS {
		t.Errorf("Automation webhook p50 = %.3fms, budget <= %.3fms", p50, automationPhase3P50BudgetMS)
	}
	if p95 > automationPhase3P95BudgetMS {
		t.Errorf("Automation webhook p95 = %.3fms, budget <= %.3fms", p95, automationPhase3P95BudgetMS)
	}
}

type automationPhase3DeleteResult struct {
	BindingID     string `json:"binding_id"`
	Deleted       bool   `json:"deleted"`
	GrantsRevoked int    `json:"grants_revoked"`
}

func (e *githubWebhookE2E) phase3RuntimeDir() string {
	return filepath.Join(e.configDir, "runtime")
}

func (e *githubWebhookE2E) phase3HomeDir() string {
	return filepath.Join(e.configDir, "home")
}

func (e *githubWebhookE2E) seedAutomationPhase3Target() {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	driver, err := e.fleetClient.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: e.workspace,
		DriverID:     automationPhase3DriverID,
		Name:         automationPhase3DriverID,
		OwnerType:    domain.DriverOwnerUser,
		OwnerRef:     e.actor,
		Status:       domain.DriverStatusDraft,
		TrustLevel:   domain.DriverTrustUntrusted,
	})
	if err != nil {
		e.t.Fatalf("seed Phase 3 driver: %v", err)
	}
	if driver.Revision != 1 {
		e.t.Fatalf("seed Phase 3 driver revision = %d, want 1", driver.Revision)
	}
	version, err := e.fleetClient.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     e.workspace,
		VersionID:        automationPhase3VersionID,
		DriverID:         automationPhase3DriverID,
		Version:          1,
		SourceRef:        "e2e://automation-phase3-source",
		SourceDigest:     "sha256:automation-phase3-source",
		BundleRef:        "e2e://automation-phase3-bundle",
		BundleDigest:     "sha256:automation-phase3-bundle",
		Runtime:          "flue-node",
		Manifest:         map[string]string{workflowcatalog.ManifestTrustLevelKey: string(domain.DriverTrustUntrusted)},
		ValidationStatus: domain.DriverVersionValidationPassed,
		CreatedBy:        e.actor,
	})
	if err != nil {
		e.t.Fatalf("seed Phase 3 driver version: %v", err)
	}
	if version.DriverID != automationPhase3DriverID || version.ValidationStatus != domain.DriverVersionValidationPassed {
		e.t.Fatalf("seed Phase 3 version = %+v", version)
	}
	approved, err := e.fleetClient.WorkflowCatalog().ApproveVersion(
		ctx, e.workspace, automationPhase3DriverID, automationPhase3VersionID, 1,
	)
	if err != nil || approved == nil || approved.CommittedRevision != 2 {
		e.t.Fatalf("approve Phase 3 fixture = %+v, %v", approved, err)
	}
	activated, err := e.fleetClient.WorkflowCatalog().ActivateVersion(
		ctx, e.workspace, automationPhase3DriverID, automationPhase3VersionID, 2,
	)
	if err != nil || activated == nil || activated.CommittedRevision != 3 || activated.Driver == nil || activated.Driver.ActiveVersionID != automationPhase3VersionID {
		e.t.Fatalf("activate Phase 3 fixture = %+v, %v", activated, err)
	}
}

func (e *githubWebhookE2E) automationPhase3CreateBindingBody() map[string]any {
	enabled := true
	return map[string]any{
		"binding_id":        automationPhase3BindingID,
		"name":              "Phase 3 GitHub PR review",
		"source_kind":       "github",
		"route_key":         "github.pull_request.opened",
		"driver_id":         automationPhase3DriverID,
		"driver_version_id": automationPhase3VersionID,
		"entrypoint":        "run",
		"secret":            automationPhase3WebhookSecret,
		"enabled":           enabled,
	}
}

func (e *githubWebhookE2E) runAutomationPhase3CLIJSON(output any, args ...string) {
	e.t.Helper()
	commandArgs := append([]string{"--server", e.loomURL, "--workspace", e.workspace}, args...)
	cmd := exec.Command(e.loomBin, commandArgs...)
	cmd.Dir = e.workDir
	cmd.Env = githubWebhookEnv(map[string]string{
		"HOME":                       e.phase3HomeDir(),
		"LOOM_CONFIG_DIR":            e.configDir,
		"LOOM_WORKSPACE_RUNTIME_DIR": e.phase3RuntimeDir(),
		"LOOM_SERVER_URL":            "",
		"LOOM_WORKSPACE":             "",
		"LOOM_FLEET_DB_URL":          "http://127.0.0.1:1",
		"LOOM_FLEET_URL":             "",
		bootstrap.EnvFleetDBBin:      filepath.Join(e.t.TempDir(), "must-not-start-fleet-db"),
		bootstrap.EnvFleetDBAPIKey:   "",
		bootstrap.EnvFleetDBActor:    "must-not-reach-fleet-db",
	})
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		e.t.Fatalf("standalone CLI loom %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(commandArgs, " "), err, stdout, stderr.String())
	}
	if err := json.Unmarshal(stdout, output); err != nil {
		e.t.Fatalf("decode standalone CLI loom %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(commandArgs, " "), err, stdout, stderr.String())
	}
}

func (e *githubWebhookE2E) doAutomationPhase3LoomJSON(method, path string, input any, bearer string, wantStatus int, output any) {
	e.t.Helper()
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			e.t.Fatalf("encode %s %s: %v", method, path, err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, e.loomURL+path, body)
	if err != nil {
		e.t.Fatalf("create %s %s: %v", method, path, err)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	githubWebhookDecodeResponse(e.t, resp, wantStatus, output)
}

func (e *githubWebhookE2E) assertAutomationPhase3BindingDeleted(bindingID string) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := e.fleetClient.TriggerBindings().Get(ctx, e.workspace, bindingID); !errors.Is(err, domain.ErrNotFound) {
		e.t.Fatalf("durable binding after delete = %v, want not found", err)
	}
}

func assertAutomationPhase3Binding(t *testing.T, binding *domain.TriggerBinding, bindingID string, policy domain.TriggerBindingConcurrencyPolicy) {
	t.Helper()
	if binding == nil || binding.BindingID != bindingID || binding.DriverID != automationPhase3DriverID ||
		binding.DriverVersionID != automationPhase3VersionID || binding.RouteKey != "github.pull_request.opened" ||
		binding.SourceKind != "github" || !binding.Enabled || binding.ConcurrencyPolicy != policy {
		t.Fatalf("binding = %+v, want enabled %s -> %s/%s policy %s", binding, bindingID, automationPhase3DriverID, automationPhase3VersionID, policy)
	}
}

func assertAutomationPhase3BindingListed(t *testing.T, bindings []*domain.TriggerBinding, bindingID string) {
	t.Helper()
	for _, binding := range bindings {
		if binding != nil && binding.BindingID == bindingID {
			return
		}
	}
	t.Fatalf("binding %s not found in %+v", bindingID, bindings)
}

func assertAutomationPhase3Run(t *testing.T, run *domain.DriverRun, bindingID string) {
	t.Helper()
	if run == nil || strings.TrimSpace(run.RunID) == "" || run.TriggerBindingID != bindingID ||
		run.DriverID != automationPhase3DriverID || run.DriverVersionID != automationPhase3VersionID ||
		run.Status != domain.DriverRunQueued {
		t.Fatalf("manual run = %+v, want queued %s/%s for %s", run, automationPhase3DriverID, automationPhase3VersionID, bindingID)
	}
}

func assertAutomationPhase3EventListed(t *testing.T, events []*domain.TriggerEvent, eventID string) {
	t.Helper()
	for _, event := range events {
		if event != nil && event.EventID == eventID {
			return
		}
	}
	t.Fatalf("event %s not found in %+v", eventID, events)
}

func assertAutomationPhase3DeliveryListed(t *testing.T, deliveries []*domain.TriggerDelivery, eventID, runID string) *domain.TriggerDelivery {
	t.Helper()
	for _, delivery := range deliveries {
		if delivery != nil && delivery.TriggerEventID == eventID && delivery.DriverRunID == runID {
			return delivery
		}
	}
	t.Fatalf("delivery for event %s run %s not found in %+v", eventID, runID, deliveries)
	return nil
}

type automationPhase3FleetRequestCounter struct {
	mu       sync.Mutex
	requests []string
}

func (counter *automationPhase3FleetRequestCounter) Record(request *http.Request) {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	counter.requests = append(counter.requests, request.Method+" "+request.URL.EscapedPath())
}

func (counter *automationPhase3FleetRequestCounter) Snapshot() int {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	return len(counter.requests)
}

func (counter *automationPhase3FleetRequestCounter) Since(snapshot int) []string {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	return append([]string(nil), counter.requests[snapshot:]...)
}

func startAutomationPhase3CountingProxy(t *testing.T, upstream string) (string, *automationPhase3FleetRequestCounter) {
	t.Helper()
	target, err := url.Parse(upstream)
	if err != nil {
		t.Fatalf("parse FleetDB upstream %q: %v", upstream, err)
	}
	requests := &automationPhase3FleetRequestCounter{}
	proxy := httputil.NewSingleHostReverseProxy(target)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Record(request)
		proxy.ServeHTTP(w, request)
	}))
	t.Cleanup(server.Close)
	return server.URL, requests
}

func automationPhase3NearestRankMS(samples []float64, percentile float64) float64 {
	if len(samples) == 0 || percentile <= 0 || percentile > 1 {
		return 0
	}
	ordered := append([]float64(nil), samples...)
	sort.Float64s(ordered)
	rank := int(math.Ceil(percentile * float64(len(ordered))))
	return ordered[rank-1]
}

func automationPhase3FormatSamplesMS(samples []float64) string {
	formatted := make([]string, 0, len(samples))
	for _, sample := range samples {
		formatted = append(formatted, strconv.FormatFloat(sample, 'f', 3, 64))
	}
	return strings.Join(formatted, ", ")
}
