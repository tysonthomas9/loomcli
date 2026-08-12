//go:build e2e
// +build e2e

package workflows

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	issuefleet "github.com/tysonthomas9/loomcli/internal/modules/workitems/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/taskrunapi"
)

const (
	phase4TaskDesignSamples = 30
	// Two workspace-scope guard reads precede the one atomic design command.
	phase4TaskDesignFleetRoundTrips = 3
	phase4TaskDesignP50BudgetMS     = 35.0
	phase4TaskDesignP95BudgetMS     = 75.0
)

// TestE2E_TaskRunPhase4WorkItemDesignPerformance measures the real
// lease-authenticated task-design-update facade through loom serve and the
// paired FleetDB checkout. Fixture creation and the final durability read use
// clients bound directly to FleetDB, outside both the timed samples and the
// counting proxy. Each measured request carries no caller-selected Work Item
// ID: FleetDB derives it from the owner-fenced TaskRun.
func TestE2E_TaskRunPhase4WorkItemDesignPerformance(t *testing.T) {
	if strings.TrimSpace(os.Getenv("FLEET_DB_REPO")) == "" {
		t.Skip("set FLEET_DB_REPO to the paired FleetDB checkout")
	}

	e2e := newWorkflowCatalogPhase2E2E(t)
	// Exercise the production-safe managed design representation. The embedded
	// launcher supplies an isolated local content directory; Fleet commits the
	// finalized artifact metadata and Issue pointer under the TaskRun fence.
	t.Setenv("FLEETDB_ISSUE_DESIGN_STORAGE", "artifact")
	e2e.startFleetDB()
	e2e.seedPrerequisites()
	fixture := seedPhase4TaskDesignFixture(t, e2e)

	proxyURL, fleetRequests := startWorkflowCatalogCountingProxy(t, e2e.fleetURL)
	e2e.fleetURL = proxyURL
	e2e.startLoomServe()

	durationsMS := make([]float64, 0, phase4TaskDesignSamples)
	lastDesign := ""
	for sample := 0; sample < phase4TaskDesignSamples; sample++ {
		requestID := fmt.Sprintf("phase4-task-design-%02d", sample+1)
		lastDesign = fmt.Sprintf("# Phase 4 plan %02d\n\nPersist distinct planner design %02d.", sample+1, sample+1)
		beforeRequests := fleetRequests.Snapshot()
		started := time.Now()
		result := postPhase4TaskDesign(t, e2e, fixture, requestID, lastDesign)
		elapsed := time.Since(started)
		trace := fleetRequests.Since(beforeRequests)

		if result.TaskID != fixture.workItemID || result.ActionID != "task-run-work-item-design-update:"+requestID || result.Replayed {
			t.Fatalf("design sample %d result = %+v, want Work Item %s with a fresh exact action receipt", sample+1, result, fixture.workItemID)
		}
		if got := len(trace); got != phase4TaskDesignFleetRoundTrips {
			t.Fatalf("design sample %d Loom-to-FleetDB round trips = %d, want exactly %d; trace=%v", sample+1, got, phase4TaskDesignFleetRoundTrips, trace)
		}
		if sample == 0 {
			t.Logf("TaskRun Work Item design Loom-to-FleetDB request trace: %v", trace)
		}
		durationsMS = append(durationsMS, float64(elapsed)/float64(time.Millisecond))

		// Loom's production mutation limiter admits 20 requests/second. Pace
		// samples below that boundary, after this sample's timing and request
		// trace have closed and before the next snapshots are taken.
		if sample+1 < phase4TaskDesignSamples {
			time.Sleep(60 * time.Millisecond)
		}
	}

	p50 := nearestRankDurationMS(durationsMS, 0.50)
	p95 := nearestRankDurationMS(durationsMS, 0.95)
	t.Logf("TaskRun Work Item design raw durations (ms, n=%d): [%s]", len(durationsMS), formatDurationSamplesMS(durationsMS))
	t.Logf("TaskRun Work Item design nearest-rank latency: p50=%.3fms p95=%.3fms; Loom-to-FleetDB round trips=%d per sample", p50, p95, phase4TaskDesignFleetRoundTrips)
	if p50 > phase4TaskDesignP50BudgetMS {
		t.Errorf("TaskRun Work Item design p50 = %.3fms, budget <= %.3fms", p50, phase4TaskDesignP50BudgetMS)
	}
	if p95 > phase4TaskDesignP95BudgetMS {
		t.Errorf("TaskRun Work Item design p95 = %.3fms, budget <= %.3fms", p95, phase4TaskDesignP95BudgetMS)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	persisted, err := fixture.issues.Get(ctx, workitems.GetQuery{IssueID: fixture.workItemID})
	if err != nil {
		t.Fatalf("read final durable Work Item directly from FleetDB: %v", err)
	}
	if persisted.Design != lastDesign || persisted.DesignFormat != "markdown" ||
		!persisted.HasDesign || strings.TrimSpace(persisted.DesignArtifactID) == "" {
		t.Fatalf("final durable Work Item design = %q format=%q artifact=%q hasDesign=%t, want managed final sample %q in markdown", persisted.Design, persisted.DesignFormat, persisted.DesignArtifactID, persisted.HasDesign, lastDesign)
	}
}

type phase4TaskDesignFixture struct {
	workItemID   string
	taskRunID    string
	nodeID       string
	leaseID      string
	leaseToken   string
	fencingToken int64
	issues       workitems.API
}

func seedPhase4TaskDesignFixture(t *testing.T, e2e *workflowCatalogPhase2E2E) phase4TaskDesignFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	issues, err := issuefleet.New(issuefleet.Config{
		BaseURL: e2e.fleetURL, WorkspaceID: e2e.workspace,
		APIKey: e2e.fleetAPIKey, Actor: e2e.actor,
	})
	if err != nil {
		t.Fatalf("create direct FleetDB Issue backend: %v", err)
	}
	workItemAPI, err := workitems.New(issues)
	if err != nil {
		t.Fatalf("compose direct FleetDB Work Items: %v", err)
	}
	workItem, err := workItemAPI.Create(ctx, workitems.CreateCommand{
		Title: "Plan the Phase 4 TaskRun facade", Status: "open", IssueType: "task", Priority: 2,
		IdempotencyKey: "phase4-task-design-performance-work-item",
	})
	if err != nil {
		t.Fatalf("create Work Item fixture: %v", err)
	}

	const (
		driverRunID   = "phase4-task-design-driver-run"
		driverNodeID  = "phase4-task-design-driver-node"
		driverLeaseID = "phase4-task-design-driver-lease"
		driverToken   = "phase4-task-design-driver-token"
		taskNodeID    = "phase4-task-design-task-node"
		taskLeaseID   = "phase4-task-design-task-lease"
		taskToken     = "phase4-task-design-task-token"
	)
	if _, err := e2e.fleetClient.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey: e2e.workspace, NodeID: taskNodeID, OwnerActor: e2e.actor,
		RuntimeProvider: domain.RuntimeProviderLocal, DrainState: domain.NodeDrainActive,
		Capacity: 2, TTL: 15 * time.Minute,
	}); err != nil {
		t.Fatalf("create TaskRun node fixture: %v", err)
	}
	if _, err := e2e.fleetClient.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: e2e.workspace, RunID: driverRunID,
		DriverID: e2e.driverID, DriverVersionID: e2e.versionID,
		Entrypoint: "run", SourceKind: "phase4-e2e", SourceRef: "task-design-performance",
		IdempotencyKey: "phase4-task-design-driver-run",
	}); err != nil {
		t.Fatalf("create DriverRun fixture: %v", err)
	}
	parent, err := e2e.fleetClient.Execution().ClaimDriverRun(ctx, fleetdb.ExecutionDriverRunClaimCommand{
		WorkspaceKey: e2e.workspace, RequestID: "phase4-task-design-driver-claim",
		RunID: driverRunID, NodeID: driverNodeID, LeaseID: driverLeaseID, LeaseToken: driverToken,
	})
	if err != nil {
		t.Fatalf("claim DriverRun fixture: %v", err)
	}

	claimRequestID := execution.ClaimDriverRunWorkItemRequestID(driverRunID, workItem.ID)
	claim, err := e2e.fleetClient.Execution().ClaimDriverRunWorkItem(ctx, fleetdb.ExecutionDriverRunWorkItemClaimCommand{
		WorkspaceKey: e2e.workspace, CommandID: claimRequestID,
		RunID: driverRunID, TaskID: workItem.ID,
		NodeID: parent.NodeID, LeaseID: parent.LeaseID, LeaseToken: driverToken, FencingToken: parent.FencingToken,
		ClaimTTL: 10 * time.Minute, ClaimedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("claim Work Item fixture through DriverRun: %v", err)
	}
	if claim.Action == nil || claim.Action.ActionID != execution.DriverRunWorkItemClaimActionID(claimRequestID) {
		t.Fatalf("Work Item claim receipt = %+v", claim)
	}

	taskRunID := execution.RequestedTaskRunID(driverRunID, workItem.ID)
	driverStepID := execution.RequestedDriverStepID(driverRunID, taskRunID)
	requestID := execution.RequestTaskRunRequestID(driverRunID, taskRunID)
	requested, err := e2e.fleetClient.Execution().RequestTaskRun(ctx, fleetdb.ExecutionTaskRunRequestCommand{
		WorkspaceKey: e2e.workspace, CommandID: requestID,
		TaskRunID: taskRunID, DriverRunID: driverRunID, DriverStepID: driverStepID,
		TaskID: workItem.ID, ClaimActionID: claim.Action.ActionID,
		NodeID: parent.NodeID, LeaseID: parent.LeaseID, LeaseToken: driverToken, FencingToken: parent.FencingToken,
		RequestedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("request typed TaskRun fixture: %v", err)
	}
	if requested.TaskRun == nil || requested.TaskRun.Status != domain.TaskRunQueued {
		t.Fatalf("requested TaskRun fixture = %+v, want queued", requested)
	}

	started, err := e2e.fleetClient.Execution().ClaimAndStartTaskRun(ctx, fleetdb.ExecutionClaimAndStartCommand{
		WorkspaceKey: e2e.workspace, CommandID: "phase4-task-design-task-start",
		TaskRunID: taskRunID, NodeID: taskNodeID, RunnerID: "phase4-task-design-runner",
		LeaseID: taskLeaseID, LeaseToken: taskToken, ClaimTTL: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("claim and start TaskRun fixture: %v", err)
	}
	if started.TaskRun == nil || started.TaskRun.Status != domain.TaskRunRunning || started.TaskRun.TaskID != workItem.ID || started.TaskRun.FencingToken <= 0 {
		t.Fatalf("started TaskRun fixture = %+v, want running TaskRun bound to %s", started, workItem.ID)
	}

	return phase4TaskDesignFixture{
		workItemID: workItem.ID, taskRunID: taskRunID,
		nodeID: taskNodeID, leaseID: taskLeaseID, leaseToken: taskToken,
		fencingToken: started.TaskRun.FencingToken, issues: workItemAPI,
	}
}

type phase4TaskDesignResult struct {
	TaskID   string `json:"taskId"`
	ActionID string `json:"actionId"`
	Replayed bool   `json:"replayed"`
}

func postPhase4TaskDesign(
	t *testing.T,
	e2e *workflowCatalogPhase2E2E,
	fixture phase4TaskDesignFixture,
	requestID string,
	design string,
) phase4TaskDesignResult {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"requestId": requestID, "design": design, "designFormat": "markdown",
	})
	if err != nil {
		t.Fatalf("encode task design request: %v", err)
	}
	req, err := http.NewRequest(
		http.MethodPost,
		e2e.loomURL+"/api/workspaces/"+e2e.workspace+"/task-run/task-design-update",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("create task design request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+fixture.leaseToken)
	req.Header.Set(taskrunapi.HeaderTaskRunID, fixture.taskRunID)
	req.Header.Set(taskrunapi.HeaderTaskRunNodeID, fixture.nodeID)
	req.Header.Set(taskrunapi.HeaderTaskRunLeaseID, fixture.leaseID)
	req.Header.Set(taskrunapi.HeaderTaskRunFencingToken, fmt.Sprintf("%d", fixture.fencingToken))
	response, err := e2e.httpClient.Do(req)
	if err != nil {
		t.Fatalf("post task design update: %v", err)
	}
	defer response.Body.Close()
	var result phase4TaskDesignResult
	workflowEndpointDecodeResponse(t, response, http.StatusOK, &result)
	return result
}
