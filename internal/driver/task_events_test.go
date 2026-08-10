package driver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestEnqueueTaskRunWithResultEmitsQueuedEvent(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	registerTaskWorkerNode(t, ctx, st, "event-node-1", []string{"local-noop"}, nil)

	outcome, err := EnqueueTaskRunWithResult(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:    "TEST",
		DriverRunID:     run.RunID,
		TaskRunID:       "task-run-events-queued",
		TaskID:          "TEST-EVT-1",
		ProviderProfile: "local-noop",
		ParentNodeID:    run.NodeID,
		ParentLeaseID:   run.LeaseID,
		ParentFence:     run.FencingToken,
	}, LocalTaskExecutor{})
	if err != nil {
		t.Fatalf("EnqueueTaskRunWithResult: %v", err)
	}

	events := listTaskRunEvents(t, ctx, st)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 queued event", len(events))
	}
	event := events[0]
	if event.Type != domain.TaskRunEventQueued || event.TaskRunID != outcome.Run.TaskRunID {
		t.Fatalf("event = %+v, want taskRunQueued for %q", event, outcome.Run.TaskRunID)
	}
	if event.EventID != "task-run-events-queued#0#taskRunQueued" || event.Attempt != 0 {
		t.Fatalf("event id/attempt = %q/%d, want deterministic attempt-0 id", event.EventID, event.Attempt)
	}
	if event.EpicID != "TEST-EPIC" || event.DriverRunID != run.RunID || event.Status != domain.TaskRunQueued {
		t.Fatalf("event = %+v, want epic/driver-run linkage with queued status", event)
	}
	if event.NextEligibleAt != nil {
		t.Fatalf("event next eligible at = %v, want nil on queued events", event.NextEligibleAt)
	}
}

func TestClaimAndExecuteTaskRunEmitsLifecycleEvents(t *testing.T) {
	cases := []struct {
		name            string
		execResult      TaskExecResult
		maxAttempts     int
		bindLead        bool
		wantTypes       []domain.TaskRunEventType
		wantLastAttempt int
		wantOutboxRows  int
		wantDedupeKey   string
		wantBlockedTask bool
	}{
		{
			name: "complete with lead creates outbox row",
			execResult: TaskExecResult{
				Status:       domain.TaskRunCompleted,
				LogsRef:      "logs://evt",
				ArtifactsRef: "artifacts://evt",
			},
			maxAttempts:     1,
			bindLead:        true,
			wantTypes:       []domain.TaskRunEventType{domain.TaskRunEventClaimed, domain.TaskRunEventCompleted},
			wantLastAttempt: 0,
			wantOutboxRows:  1,
			wantDedupeKey:   ":completed",
		},
		{
			name:            "complete without lead skips outbox row",
			execResult:      TaskExecResult{Status: domain.TaskRunCompleted, LogsRef: "logs://evt"},
			maxAttempts:     1,
			wantTypes:       []domain.TaskRunEventType{domain.TaskRunEventClaimed, domain.TaskRunEventCompleted},
			wantLastAttempt: 0,
		},
		{
			name: "fail with retry emits requeued",
			execResult: TaskExecResult{
				Status:     domain.TaskRunFailed,
				ExitCode:   3,
				ErrorClass: "task_failed",
			},
			maxAttempts:     2,
			bindLead:        true,
			wantTypes:       []domain.TaskRunEventType{domain.TaskRunEventClaimed, domain.TaskRunEventRequeued},
			wantLastAttempt: 1,
		},
		{
			name: "exhaust attempts blocks task and creates outbox row",
			execResult: TaskExecResult{
				Status:     domain.TaskRunFailed,
				ExitCode:   3,
				ErrorClass: "task_failed",
			},
			maxAttempts:     1,
			bindLead:        true,
			wantTypes:       []domain.TaskRunEventType{domain.TaskRunEventClaimed, domain.TaskRunEventFailed},
			wantLastAttempt: 1,
			wantOutboxRows:  1,
			wantDedupeKey:   ":failed",
			wantBlockedTask: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, st, run := setupRunningDriverRun(t)
			if tc.bindLead {
				bindEpicLead(t, ctx, st, "epic-lead-1")
			}
			taskRunID := "task-run-events-claim"
			createQueuedEventTaskRun(t, ctx, st, run.RunID, taskRunID)

			_, err := ClaimAndExecuteTaskRunWithResult(ctx, st, TaskRunWorkerOptions{
				WorkspaceKey:       "TEST",
				TaskRunID:          taskRunID,
				NodeID:             "node-1",
				SupportedProviders: []string{"local-noop"},
				HeartbeatInterval:  -1,
				MaxAttempts:        tc.maxAttempts,
			}, &recordingTaskExecutor{result: tc.execResult})
			if err != nil {
				t.Fatalf("ClaimAndExecuteTaskRunWithResult: %v", err)
			}

			events := listTaskRunEvents(t, ctx, st)
			if len(events) != len(tc.wantTypes) {
				t.Fatalf("events = %+v, want types %v", events, tc.wantTypes)
			}
			for i, want := range tc.wantTypes {
				if events[i].Type != want {
					t.Fatalf("event[%d].Type = %q, want %q", i, events[i].Type, want)
				}
				if events[i].EpicID != "TEST-EPIC" || events[i].TaskRunID != taskRunID {
					t.Fatalf("event[%d] = %+v, want epic-bound event for %q", i, events[i], taskRunID)
				}
			}
			last := events[len(events)-1]
			if last.Attempt != tc.wantLastAttempt {
				t.Fatalf("last event attempt = %d, want %d", last.Attempt, tc.wantLastAttempt)
			}
			if last.LogsRef != tc.execResult.LogsRef {
				t.Fatalf("last event logs ref = %q, want %q", last.LogsRef, tc.execResult.LogsRef)
			}
			if last.ErrorClass != tc.execResult.ErrorClass {
				t.Fatalf("last event error class = %q, want %q", last.ErrorClass, tc.execResult.ErrorClass)
			}
			if last.Type == domain.TaskRunEventRequeued && (last.NextEligibleAt == nil || !last.NextEligibleAt.After(time.Now().Add(-time.Minute))) {
				t.Fatalf("requeued event next eligible at = %v, want backoff timestamp", last.NextEligibleAt)
			}
			if last.Type != domain.TaskRunEventRequeued && last.NextEligibleAt != nil {
				t.Fatalf("event next eligible at = %v, want nil on non-requeued events", last.NextEligibleAt)
			}

			if got := st.TaskBlocked("TEST", "TEST-EVT-2"); got != tc.wantBlockedTask {
				t.Fatalf("task blocked = %v, want %v", got, tc.wantBlockedTask)
			}

			rows := listOutboxRows(t, ctx, st)
			if len(rows) != tc.wantOutboxRows {
				t.Fatalf("outbox rows = %+v, want %d", rows, tc.wantOutboxRows)
			}
			if tc.wantOutboxRows == 0 {
				return
			}
			row := rows[0]
			wantKey := "lead-task-message:TEST-EPIC:" + taskRunID + tc.wantDedupeKey
			if row.DedupeKey != wantKey {
				t.Fatalf("outbox dedupe key = %q, want %q", row.DedupeKey, wantKey)
			}
			if row.Kind != domain.OutboxKindLeadTaskMessage || row.TargetAgent != "epic-lead-1" || row.EpicID != "TEST-EPIC" {
				t.Fatalf("outbox row = %+v, want leadTaskMessage targeting epic-lead-1", row)
			}
			if !strings.Contains(row.Body, "Do not start another epic runner.") {
				t.Fatalf("outbox body = %q, want epic-runner guardrail", row.Body)
			}
			if !strings.Contains(row.Body, "task_run: "+taskRunID) {
				t.Fatalf("outbox body = %q, want task_run reference", row.Body)
			}
		})
	}
}

func TestEmitTerminalTaskRunEventsIsIdempotent(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	bindEpicLead(t, ctx, st, "epic-lead-1")
	taskRunID := "task-run-events-dedupe"
	createQueuedEventTaskRun(t, ctx, st, run.RunID, taskRunID)

	outcome, err := ClaimAndExecuteTaskRunWithResult(ctx, st, TaskRunWorkerOptions{
		WorkspaceKey:       "TEST",
		TaskRunID:          taskRunID,
		NodeID:             "node-1",
		SupportedProviders: []string{"local-noop"},
		HeartbeatInterval:  -1,
	}, &recordingTaskExecutor{result: TaskExecResult{Status: domain.TaskRunCompleted, LogsRef: "logs://evt"}})
	if err != nil {
		t.Fatalf("ClaimAndExecuteTaskRunWithResult: %v", err)
	}

	// Re-running the terminal emission path (e.g. a replayed completion)
	// must not double-append events or double-create outbox rows.
	emitTerminalTaskRunEvents(ctx, st, outcome.Run, taskExecCompletion{Status: domain.TaskRunCompleted}, taskRunEventContext{EpicID: "TEST-EPIC"})

	events := listTaskRunEvents(t, ctx, st)
	if len(events) != 2 {
		t.Fatalf("events = %+v, want claimed+completed only after replay", events)
	}
	rows := listOutboxRows(t, ctx, st)
	if len(rows) != 1 {
		t.Fatalf("outbox rows = %+v, want a single deduped row after replay", rows)
	}
}

func TestBuildLeadTaskMessage(t *testing.T) {
	cases := []struct {
		name         string
		title        string
		logsRef      string
		artifactsRef string
		status       domain.TaskRunStatus
		wantContains []string
		wantOmits    []string
	}{
		{
			name:         "completed with refs and title",
			title:        "Wire emission hooks",
			logsRef:      "logs://run",
			artifactsRef: "artifacts://run",
			status:       domain.TaskRunCompleted,
			wantContains: []string{
				"Loom completed a child task",
				"epic: EPIC-1",
				"task: TASK-1 - Wire emission hooks",
				"task_run: run-1",
				"logs: logs://run",
				"artifacts: artifacts://run",
				"Do not start another epic runner.",
			},
		},
		{
			name:   "blocked without refs",
			status: domain.TaskRunFailed,
			wantContains: []string{
				"Loom blocked a child task",
				"task: TASK-1",
				"Do not start another epic runner.",
			},
			wantOmits: []string{"logs:", "artifacts:"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := buildLeadTaskMessage("EPIC-1", "TASK-1", tc.title, "run-1", tc.logsRef, tc.artifactsRef, tc.status)
			for _, want := range tc.wantContains {
				if !strings.Contains(body, want) {
					t.Fatalf("body = %q, want substring %q", body, want)
				}
			}
			for _, omit := range tc.wantOmits {
				if strings.Contains(body, omit) {
					t.Fatalf("body = %q, want %q omitted", body, omit)
				}
			}
		})
	}
}

func TestResolveEpicLead(t *testing.T) {
	type agentFixture struct {
		name, role, parent string
	}
	cases := []struct {
		name   string
		agents []agentFixture
		want   string
	}{
		{name: "no agents", want: ""},
		{
			name: "lead bound to epic",
			agents: []agentFixture{
				{name: "dev-1", role: "developer", parent: "TEST-EPIC"},
				{name: "lead-z", role: "Lead", parent: "TEST-EPIC"},
			},
			want: "lead-z",
		},
		{
			name: "orchestrator counts as lead",
			agents: []agentFixture{
				{name: "orc-1", role: "orchestrator", parent: "TEST-EPIC"},
			},
			want: "orc-1",
		},
		{
			name: "lead bound to another epic ignored",
			agents: []agentFixture{
				{name: "lead-other", role: "lead", parent: "OTHER-EPIC"},
			},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, st, _ := setupRunningDriverRun(t)
			for _, in := range tc.agents {
				createEventRole(t, ctx, st, in.role)
				profileID := in.name + "-profile"
				if _, err := st.WorkerProfiles().Create(ctx, store.WorkerProfileCreate{
					WorkspaceKey: "TEST", ProfileID: profileID, Name: profileID, Role: in.role, ParentEpic: in.parent,
				}); err != nil {
					t.Fatalf("Create profile %q: %v", profileID, err)
				}
				if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
					WorkspaceKey: "TEST", ServiceID: in.name, Name: in.name, RoleName: in.role,
					ProfileName: profileID, Kind: domain.AgentServiceKindLead,
					DesiredState: domain.AgentServiceDesiredRunning, MaxInstances: 1,
				}); err != nil {
					t.Fatalf("Create agent service %q: %v", in.name, err)
				}
			}
			got, err := resolveEpicLead(ctx, st, "TEST", "TEST-EPIC")
			if err != nil {
				t.Fatalf("resolveEpicLead: %v", err)
			}
			if got != tc.want {
				t.Fatalf("resolveEpicLead = %q, want %q", got, tc.want)
			}
		})
	}
}

func createQueuedEventTaskRun(t *testing.T, ctx context.Context, st store.Store, driverRunID, taskRunID string) {
	t.Helper()
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:    "TEST",
		TaskRunID:       taskRunID,
		DriverRunID:     driverRunID,
		TaskID:          "TEST-EVT-2",
		ProviderProfile: "local-noop",
		Status:          domain.TaskRunQueued,
	}); err != nil {
		t.Fatalf("Create queued task run: %v", err)
	}
}

func bindEpicLead(t *testing.T, ctx context.Context, st store.Store, name string) {
	t.Helper()
	createEventRole(t, ctx, st, "lead")
	profileID := name + "-profile"
	if _, err := st.WorkerProfiles().Create(ctx, store.WorkerProfileCreate{
		WorkspaceKey: "TEST", ProfileID: profileID, Name: profileID, Role: "lead", ParentEpic: "TEST-EPIC",
	}); err != nil {
		t.Fatalf("Create lead profile: %v", err)
	}
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "TEST", ServiceID: name, Name: name, RoleName: "lead", ProfileName: profileID,
		Kind: domain.AgentServiceKindLead, DesiredState: domain.AgentServiceDesiredRunning, MaxInstances: 1,
	}); err != nil {
		t.Fatalf("Create lead agent service: %v", err)
	}
}

func createEventRole(t *testing.T, ctx context.Context, st store.Store, name string) {
	t.Helper()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "TEST", Name: name}); err != nil {
		t.Fatalf("Create role %q: %v", name, err)
	}
}

func listTaskRunEvents(t *testing.T, ctx context.Context, st store.Store) []*domain.TaskRunEvent {
	t.Helper()
	events, err := st.TaskRunEvents().ListSince(ctx, "TEST", store.TaskRunEventFilter{})
	if err != nil {
		t.Fatalf("ListSince task run events: %v", err)
	}
	return events
}

func listOutboxRows(t *testing.T, ctx context.Context, st store.Store) []*domain.OutboxRecord {
	t.Helper()
	rows, err := st.Outbox().ListDue(ctx, "TEST", store.OutboxDueFilter{Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("ListDue outbox: %v", err)
	}
	return rows
}
