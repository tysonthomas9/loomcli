//go:build e2e
// +build e2e

package workflows

import (
	"math"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

const (
	workflowCatalogApproveSamples         = 30
	workflowCatalogApproveFleetRoundTrips = 6
	workflowCatalogApproveP50BudgetMS     = 25.0
	workflowCatalogApproveP95BudgetMS     = 50.0
)

// TestE2E_WorkflowCatalogPhase2ApprovePerformance measures the real HTTP
// management path through loom serve and fleet-db. A reverse proxy observes
// only Loom-to-FleetDB traffic; fixture writes and assertions use the direct
// FleetDB client and therefore cannot inflate the measured request count.
func TestE2E_WorkflowCatalogPhase2ApprovePerformance(t *testing.T) {
	e2e := newWorkflowCatalogPhase2E2E(t)
	e2e.startFleetDB()
	e2e.seedPrerequisites()

	proxyURL, fleetRequests := startWorkflowCatalogCountingProxy(t, e2e.fleetURL)
	e2e.fleetURL = proxyURL
	e2e.startLoomServe()

	revision := uint64(1)
	durationsMS := make([]float64, 0, workflowCatalogApproveSamples)
	for sample := 0; sample < workflowCatalogApproveSamples; sample++ {
		beforeRequests := fleetRequests.Snapshot()
		started := time.Now()

		var approved workflowCatalogHTTPAction
		e2e.doLoomJSON(
			http.MethodPost,
			e2e.lifecyclePath(e2e.workspace, "approve"),
			map[string]uint64{"expected_revision": revision},
			"",
			http.StatusOK,
			&approved,
		)
		elapsed := time.Since(started)
		approveRequests := fleetRequests.Since(beforeRequests)

		revision++
		if approved.Action != "approve" || approved.Driver == nil || approved.Version == nil || approved.Driver.Revision != revision || !workflowcatalog.VersionApproved(approved.Driver, approved.Version) {
			t.Fatalf("approve sample %d = %+v, want changed approval at revision %d", sample+1, approved, revision)
		}
		if got := len(approveRequests); got != workflowCatalogApproveFleetRoundTrips {
			t.Fatalf("approve sample %d Loom-to-FleetDB round trips = %d, want exactly %d", sample+1, got, workflowCatalogApproveFleetRoundTrips)
		}
		if sample == 0 {
			t.Logf("Workflow Catalog approve Loom-to-FleetDB request trace: %v", approveRequests)
		}
		durationsMS = append(durationsMS, float64(elapsed)/float64(time.Millisecond))

		// Restore an actually unapproved state between samples. The next
		// measurement takes its request and time snapshots only after this
		// reset has completed, so reset work is excluded from every sample.
		if sample+1 < workflowCatalogApproveSamples {
			var unapproved workflowCatalogHTTPAction
			e2e.doLoomJSON(
				http.MethodPost,
				e2e.lifecyclePath(e2e.workspace, "unapprove"),
				map[string]uint64{"expected_revision": revision},
				"",
				http.StatusOK,
				&unapproved,
			)
			revision++
			if unapproved.Action != "unapprove" || unapproved.Driver == nil || unapproved.Version == nil || unapproved.Driver.Revision != revision || workflowcatalog.VersionApproved(unapproved.Driver, unapproved.Version) {
				t.Fatalf("unapprove reset after sample %d = %+v, want changed unapproval at revision %d", sample+1, unapproved, revision)
			}
			// Production serve admits 20 mutations/second. Pace the reset/sample
			// pairs below that boundary; this delay occurs after reset and before
			// the next sample's timer and request-count snapshot.
			time.Sleep(110 * time.Millisecond)
		}
	}

	p50 := nearestRankDurationMS(durationsMS, 0.50)
	p95 := nearestRankDurationMS(durationsMS, 0.95)
	t.Logf("Workflow Catalog approve raw durations (ms, n=%d): [%s]", len(durationsMS), formatDurationSamplesMS(durationsMS))
	t.Logf("Workflow Catalog approve nearest-rank latency: p50=%.3fms p95=%.3fms; Loom-to-FleetDB round trips=%d per sample", p50, p95, workflowCatalogApproveFleetRoundTrips)
	if p50 > workflowCatalogApproveP50BudgetMS {
		t.Errorf("Workflow Catalog approve p50 = %.3fms, budget <= %.3fms", p50, workflowCatalogApproveP50BudgetMS)
	}
	if p95 > workflowCatalogApproveP95BudgetMS {
		t.Errorf("Workflow Catalog approve p95 = %.3fms, budget <= %.3fms", p95, workflowCatalogApproveP95BudgetMS)
	}
}

type workflowCatalogFleetRequestCounter struct {
	mu       sync.Mutex
	requests []string
}

func (c *workflowCatalogFleetRequestCounter) Record(request *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, request.Method+" "+request.URL.EscapedPath())
}

func (c *workflowCatalogFleetRequestCounter) Snapshot() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func (c *workflowCatalogFleetRequestCounter) Since(snapshot int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.requests[snapshot:]...)
}

func startWorkflowCatalogCountingProxy(t *testing.T, upstream string) (string, *workflowCatalogFleetRequestCounter) {
	t.Helper()
	target, err := url.Parse(upstream)
	if err != nil {
		t.Fatalf("parse FleetDB upstream %q: %v", upstream, err)
	}
	requests := &workflowCatalogFleetRequestCounter{}
	proxy := httputil.NewSingleHostReverseProxy(target)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Record(r)
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server.URL, requests
}

func nearestRankDurationMS(samples []float64, percentile float64) float64 {
	if len(samples) == 0 || percentile <= 0 || percentile > 1 {
		return 0
	}
	ordered := append([]float64(nil), samples...)
	sort.Float64s(ordered)
	rank := int(math.Ceil(percentile * float64(len(ordered))))
	return ordered[rank-1]
}

func formatDurationSamplesMS(samples []float64) string {
	formatted := make([]string, 0, len(samples))
	for _, sample := range samples {
		formatted = append(formatted, strconv.FormatFloat(sample, 'f', 3, 64))
	}
	return strings.Join(formatted, ", ")
}
