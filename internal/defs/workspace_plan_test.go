package defs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestPlanFromWorkspaceProjectsControlPlaneRecords(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "CP", Name: "Control Plane"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	maxConcurrency := 2
	budget := 1.5
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey:   "CP",
		Name:           "triage",
		Description:    "Directly managed triage role.",
		Model:          "gpt-5",
		Backend:        "codex",
		Skills:         []string{"review"},
		MaxConcurrency: &maxConcurrency,
		ReadOnly:       true,
		AllowedTools:   []string{"github.issue.read"},
		DeniedTools:    []string{"bash"},
		MaxBudgetUSD:   &budget,
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey:   "CP",
		Name:           "triage-bot",
		RoleName:       "triage",
		Auto:           true,
		Backend:        "codex",
		Repos:          []string{"slack-src"},
		Mode:           domain.AgentModeService,
		TaskFilter:     "needs_design",
		MaxConcurrency: 1,
		BudgetPolicy:   "p2",
		DesiredState:   domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent instance: %v", err)
	}
	active := domain.AgentStateActive
	if _, err := st.Agents().Update(ctx, "CP", "triage-bot", store.AgentUpdate{State: &active}); err != nil {
		t.Fatalf("activate agent instance: %v", err)
	}
	workflowManifest := mustJSON(map[string]any{
		"builtin":              "run-parent-work-items",
		"runtime_profile_name": "local-node",
		"tools":                []string{"workItems.readyChildren", "taskRuns.ensure"},
		"repos":                []string{"slack-src"},
		"env":                  []string{"NODE_ENV"},
	})
	if _, err := st.WorkflowDefinitions().Upsert(ctx, store.WorkflowDefinitionUpsert{
		WorkspaceKey:       "CP",
		Name:               "slack-clone-runner",
		Version:            "control-v1",
		Description:        "Run Slack clone epic tasks.",
		SingletonPolicy:    "parent:${parentId}",
		RuntimeProfileName: "local-node",
		Manifest:           workflowManifest,
		Status:             domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert workflow definition: %v", err)
	}
	if _, err := st.RouteBindings().Upsert(ctx, store.RouteBindingUpsert{
		WorkspaceKey:   "CP",
		DefinitionName: "slack-clone-runner",
		DefinitionType: domain.DefinitionTypeWorkflow,
		Path:           "/workflows/slack-clone-runner/run",
		Method:         "POST",
		AuthPolicy:     "workspace",
		Status:         domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert route binding: %v", err)
	}
	if _, err := st.TriggerBindings().Upsert(ctx, store.TriggerBindingUpsert{
		WorkspaceKey: "CP",
		WorkflowName: "slack-clone-runner",
		EventType:    "issue.label_added",
		Filter:       mustJSON(map[string]string{"label": "slack-clone", "type": "epic"}),
		Status:       domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert trigger binding: %v", err)
	}
	if _, err := st.RuntimeProfiles().Upsert(ctx, store.RuntimeProfileUpsert{
		WorkspaceKey: "CP",
		Name:         "local-node",
		Version:      "control-v1",
		Provider:     domain.RuntimeProviderLocal,
		Image:        "node:22",
		Repos:        []string{"slack-src"},
		Env:          []string{"NODE_ENV"},
		Status:       domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert runtime profile: %v", err)
	}
	tool := ToolModule{
		Name:        "github_issue_read",
		Description: "Read one GitHub issue visible to this workspace.",
		Version:     "tool-v1",
		SourcePath:  "control-plane:tool/github_issue_read",
		SourceHash:  "tool-hash",
		Parameters:  map[string]any{"kind": "object"},
		Handler:     "workflow",
		ReadOnly:    true,
	}
	if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
		WorkspaceKey:       "CP",
		DefinitionType:     domain.DefinitionTypeTool,
		DefinitionName:     tool.Name,
		Version:            tool.Version,
		SourceHash:         tool.SourceHash,
		Manifest:           mustJSON(tool),
		CapabilityManifest: toolCapabilityManifest(tool),
		Status:             domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("apply tool definition: %v", err)
	}
	startedAt := time.Date(2026, 5, 29, 18, 0, 0, 0, time.UTC)
	workflowRun, err := st.WorkflowRuns().CreateOrResume(ctx, store.WorkflowRunCreate{
		WorkspaceKey:    "CP",
		RunID:           "wrun-slack-1",
		WorkflowName:    "slack-clone-runner",
		WorkflowVersion: "control-v1",
		BundleHash:      "workflow-bundle-hash",
		IdempotencyKey:  "slack-clone-runner:EPIC-1",
		Input:           json.RawMessage(`{"parentId":"EPIC-1"}`),
		Status:          domain.WorkflowRunRunning,
		LeaseOwner:      "epic-runner",
		LeaseToken:      "lease-1",
		StartedAt:       startedAt,
	})
	if err != nil {
		t.Fatalf("create workflow run: %v", err)
	}
	waiting := domain.WorkflowRunWaiting
	waitCondition := "work_item_changed(parent:EPIC-1)"
	fencingToken := int64(7)
	partialResult := json.RawMessage(`{"ready":2}`)
	if _, err := st.WorkflowRuns().Update(ctx, "CP", workflowRun.RunID, store.WorkflowRunUpdate{
		Status:        &waiting,
		Result:        &partialResult,
		WaitCondition: &waitCondition,
		FencingToken:  &fencingToken,
	}); err != nil {
		t.Fatalf("update workflow run: %v", err)
	}
	taskStartedAt := startedAt.Add(5 * time.Minute)
	taskRun, err := st.TaskRuns().Ensure(ctx, store.TaskRunEnsure{
		WorkspaceKey:    "CP",
		TaskRunID:       "trun-slack-1",
		IdempotencyKey:  "workflow_run:wrun-slack-1:work_item:TASK-1:role:task",
		WorkflowRunID:   workflowRun.RunID,
		WorkItemID:      "TASK-1",
		RoleName:        "task",
		ClaimActor:      "triage-bot",
		ClaimEventID:    "claim-event-1",
		Status:          domain.TaskRunStarting,
		AgentID:         "triage-bot",
		NodeID:          "node-local",
		CommandID:       "cmd-1",
		SessionID:       "session-1",
		LeaseID:         "lease-task-1",
		ParentSessionID: "session-parent",
		Reason:          "Build channel sidebar",
		Metadata:        map[string]string{"surface": "sidebar", "app": "slack"},
	})
	if err != nil {
		t.Fatalf("ensure task run: %v", err)
	}
	running := domain.TaskRunRunning
	if _, err := st.TaskRuns().Update(ctx, "CP", taskRun.TaskRunID, store.TaskRunUpdate{
		Status:    &running,
		StartedAt: &taskStartedAt,
	}); err != nil {
		t.Fatalf("update task run: %v", err)
	}
	if _, err := st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  "CP",
		EventID:       "rev-slack-1",
		WorkflowRunID: workflowRun.RunID,
		TaskRunID:     taskRun.TaskRunID,
		Type:          "task_run_dispatched",
		Message:       "task run dispatched",
		Data:          json.RawMessage(`{"command_id":"cmd-1","task_run_id":"trun-slack-1"}`),
	}); err != nil {
		t.Fatalf("append run event: %v", err)
	}
	sessionHeartbeat := taskStartedAt.Add(time.Minute)
	if _, err := st.TerminalSessions().Create(ctx, store.TerminalSessionCreate{
		WorkspaceKey:    "CP",
		TerminalID:      "term-1",
		AgentID:         "triage-bot",
		SessionID:       "session-1",
		NodeID:          "node-local",
		TaskID:          "TASK-1",
		Title:           "Slack runner terminal",
		Kind:            "pty",
		Status:          domain.TerminalSessionOpen,
		PTYProvider:     "local",
		StreamRef:       "stream://term-1",
		TranscriptRef:   "transcript://term-1",
		AttachedClients: 1,
		Metadata:        map[string]string{"pane": "runner"},
	}); err != nil {
		t.Fatalf("create terminal session: %v", err)
	}
	terminalSeenAt := sessionHeartbeat.Add(time.Minute)
	if _, err := st.TerminalSessions().Update(ctx, "CP", "term-1", store.TerminalSessionUpdate{
		LastSeenAt: &terminalSeenAt,
	}); err != nil {
		t.Fatalf("update terminal session: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey:    "CP",
		SessionID:       "session-1",
		AgentID:         "triage-bot",
		NodeID:          "node-local",
		Kind:            domain.AgentSessionKindTask,
		TaskID:          "TASK-1",
		TerminalID:      "term-1",
		ParentSessionID: "session-parent",
		Status:          domain.AgentSessionRunning,
		Phase:           "implementing",
		Attempt:         2,
		Metadata:        map[string]string{"workflow_run_id": workflowRun.RunID, "harness": "planning"},
	}); err != nil {
		t.Fatalf("create agent session: %v", err)
	}
	sessionSummary := "working on sidebar"
	if _, err := st.AgentSessions().Update(ctx, "CP", "session-1", store.AgentSessionUpdate{
		LastHeartbeat: &sessionHeartbeat,
		Summary:       &sessionSummary,
	}); err != nil {
		t.Fatalf("update agent session: %v", err)
	}
	leaseCreatedAt := sessionHeartbeat.Add(-30 * time.Second)
	leaseUpdatedAt := sessionHeartbeat.Add(30 * time.Second)
	leaseExpiresAt := sessionHeartbeat.Add(10 * time.Minute)
	if _, err := st.AgentLeases().Create(ctx, store.AgentLeaseCreate{
		WorkspaceKey:  "CP",
		SessionID:     "session-1",
		LeaseID:       "lease-task-1",
		AgentID:       "triage-bot",
		NodeID:        "node-local",
		Token:         "task-token-1",
		FencingToken:  11,
		Status:        domain.AgentLeaseActive,
		ExpiresAt:     leaseExpiresAt,
		LastHeartbeat: sessionHeartbeat,
		CreatedAt:     leaseCreatedAt,
		UpdatedAt:     leaseUpdatedAt,
	}); err != nil {
		t.Fatalf("create agent lease: %v", err)
	}
	ownershipExpiresAt := sessionHeartbeat.Add(15 * time.Minute)
	if _, err := st.AgentOwnershipLeases().Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey:    "CP",
		AgentID:         "triage-bot",
		LeaseID:         "owner-lease-1",
		OwnerID:         "daemon-local",
		RuntimeProvider: domain.RuntimeProviderLocal,
		NodeID:          "node-local",
		Token:           "owner-token-1",
		FencingToken:    13,
		Status:          domain.AgentLeaseActive,
		ExpiresAt:       ownershipExpiresAt,
		LastHeartbeat:   sessionHeartbeat,
		CreatedAt:       leaseCreatedAt,
		UpdatedAt:       leaseUpdatedAt,
	}); err != nil {
		t.Fatalf("acquire agent ownership lease: %v", err)
	}
	if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "CP",
		CommandID:     "cmd-1",
		TargetAgentID: "triage-bot",
		TargetNodeID:  "node-local",
		SessionID:     "session-1",
		Type:          "start-task",
		Payload:       map[string]string{"workflow_run_id": workflowRun.RunID, "task_run_id": taskRun.TaskRunID},
	}); err != nil {
		t.Fatalf("create agent command: %v", err)
	}
	if _, err := st.AgentCommands().Complete(ctx, "CP", "cmd-1", store.AgentCommandComplete{
		Status: domain.AgentCommandSucceeded,
		Result: "started",
	}); err != nil {
		t.Fatalf("complete agent command: %v", err)
	}
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey: "CP",
		ArtifactID:   "artifact-slack-1",
		AgentID:      "triage-bot",
		SessionID:    "session-1",
		TerminalID:   "term-1",
		TaskID:       "TASK-1",
		Type:         "report",
		URI:          "artifact://workflow/wrun-slack-1/report.json",
		Summary:      "Slack runner report",
		MIMEType:     "application/json",
		SizeBytes:    512,
		Checksum:     "sha256:abc123",
		Metadata:     map[string]string{"workflow_run_id": workflowRun.RunID, "kind": "summary"},
	}); err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	plan, err := PlanFromWorkspace(ctx, st, "CP")
	if err != nil {
		t.Fatalf("PlanFromWorkspace() error = %v", err)
	}
	if got := Summary(plan); got != "agents=1 workflows=1 runtimes=1 agent_instances=1 agent_sessions=1 agent_leases=1 agent_ownership_leases=1 agent_commands=1 terminal_sessions=1 workflow_runs=1 task_runs=1 run_events=1 artifacts=1 tools=1" {
		t.Fatalf("Summary() = %q, want direct record parity", got)
	}
	if plan.Root != "workspace:CP" {
		t.Fatalf("Root = %q, want workspace source", plan.Root)
	}
	agent := plan.Agents[0]
	if agent.Name != "triage" || agent.SourcePath != "control-plane:role/triage" {
		t.Fatalf("agent = %+v, want control-plane role projection", agent)
	}
	if agent.Model != "gpt-5" || agent.Backend != "codex" || agent.MaxConcurrency != 2 || !agent.ReadOnly {
		t.Fatalf("agent policy = %+v, want direct role policy", agent)
	}
	if !strings.Contains(fmtStringSlice(agent.Tools), "github.issue.read") || !strings.Contains(fmtStringSlice(agent.DeniedCommands), "bash") {
		t.Fatalf("agent tools = %+v denied=%+v, want direct role tool policy", agent.Tools, agent.DeniedCommands)
	}
	instance := plan.AgentInstances[0]
	if instance.Name != "triage-bot" || instance.RoleName != "triage" ||
		instance.SourcePath != "control-plane:agent/triage-bot" {
		t.Fatalf("agent instance = %+v, want durable agentdef projection", instance)
	}
	if !instance.Auto || instance.State != domain.AgentStateActive ||
		instance.DesiredState != domain.AgentDesiredRunning ||
		instance.Mode != domain.AgentModeService || instance.TaskFilter != "needs_design" ||
		instance.MaxConcurrency != 1 || instance.BudgetPolicy != "p2" ||
		!strings.Contains(fmtStringSlice(instance.Repos), "slack-src") {
		t.Fatalf("agent instance policy = %+v, want durable runtime assignment", instance)
	}
	session := plan.AgentSessions[0]
	if session.SessionID != "session-1" || session.AgentID != "triage-bot" ||
		session.NodeID != "node-local" || session.Kind != domain.AgentSessionKindTask ||
		session.TaskID != "TASK-1" || session.TerminalID != "term-1" ||
		session.ParentSessionID != "session-parent" || session.Status != domain.AgentSessionRunning ||
		session.Phase != "implementing" || session.Attempt != 2 ||
		session.Summary != "working on sidebar" || session.Metadata["harness"] != "planning" ||
		session.LastHeartbeat == nil || !session.LastHeartbeat.Equal(sessionHeartbeat) {
		t.Fatalf("agent session = %+v, want durable session runtime state", session)
	}
	lease := plan.AgentLeases[0]
	if lease.LeaseID != "lease-task-1" || lease.SessionID != "session-1" ||
		lease.AgentID != "triage-bot" || lease.NodeID != "node-local" ||
		lease.Token != "task-token-1" || lease.FencingToken != 11 ||
		lease.Status != domain.AgentLeaseActive ||
		lease.ExpiresAt == nil || !lease.ExpiresAt.Equal(leaseExpiresAt) ||
		lease.LastHeartbeat == nil || !lease.LastHeartbeat.Equal(sessionHeartbeat) ||
		lease.CreatedAt == nil || !lease.CreatedAt.Equal(leaseCreatedAt) ||
		lease.UpdatedAt == nil || !lease.UpdatedAt.Equal(leaseUpdatedAt) {
		t.Fatalf("agent lease = %+v, want durable session lease runtime state", lease)
	}
	ownership := plan.AgentOwnershipLeases[0]
	if ownership.AgentID != "triage-bot" || ownership.LeaseID != "owner-lease-1" ||
		ownership.OwnerID != "daemon-local" || ownership.RuntimeProvider != domain.RuntimeProviderLocal ||
		ownership.NodeID != "node-local" || ownership.Token != "owner-token-1" ||
		ownership.FencingToken != 13 || ownership.Status != domain.AgentLeaseActive ||
		ownership.ExpiresAt == nil || !ownership.ExpiresAt.Equal(ownershipExpiresAt) ||
		ownership.LastHeartbeat == nil || !ownership.LastHeartbeat.Equal(sessionHeartbeat) {
		t.Fatalf("agent ownership lease = %+v, want durable agent ownership lease state", ownership)
	}
	command := plan.AgentCommands[0]
	if command.CommandID != "cmd-1" || command.TargetAgentID != "triage-bot" ||
		command.TargetNodeID != "node-local" || command.SessionID != "session-1" ||
		command.Type != "start-task" || command.Status != domain.AgentCommandSucceeded ||
		command.Result != "started" || command.Payload["task_run_id"] != "trun-slack-1" ||
		command.Cursor == 0 {
		t.Fatalf("agent command = %+v, want durable command routing and result state", command)
	}
	terminal := plan.TerminalSessions[0]
	if terminal.TerminalID != "term-1" || terminal.AgentID != "triage-bot" ||
		terminal.SessionID != "session-1" || terminal.NodeID != "node-local" ||
		terminal.TaskID != "TASK-1" || terminal.Title != "Slack runner terminal" ||
		terminal.Kind != "pty" || terminal.Status != domain.TerminalSessionOpen ||
		terminal.PTYProvider != "local" || terminal.StreamRef != "stream://term-1" ||
		terminal.TranscriptRef != "transcript://term-1" || terminal.AttachedClients != 1 ||
		terminal.Metadata["pane"] != "runner" || terminal.LastSeenAt == nil ||
		!terminal.LastSeenAt.Equal(terminalSeenAt) {
		t.Fatalf("terminal session = %+v, want durable terminal runtime state", terminal)
	}
	artifact := plan.Artifacts[0]
	if artifact.ArtifactID != "artifact-slack-1" || artifact.AgentID != "triage-bot" ||
		artifact.SessionID != "session-1" || artifact.TerminalID != "term-1" ||
		artifact.TaskID != "TASK-1" || artifact.Type != "report" ||
		artifact.URI != "artifact://workflow/wrun-slack-1/report.json" ||
		artifact.Summary != "Slack runner report" || artifact.MIMEType != "application/json" ||
		artifact.SizeBytes != 512 || artifact.Checksum != "sha256:abc123" ||
		artifact.Metadata["kind"] != "summary" {
		t.Fatalf("artifact = %+v, want durable artifact runtime state", artifact)
	}
	workflow := plan.Workflows[0]
	if workflow.Name != "slack-clone-runner" || workflow.Builtin != "run-parent-work-items" {
		t.Fatalf("workflow = %+v, want direct workflow projection", workflow)
	}
	if workflow.RuntimeProfileName != "local-node" {
		t.Fatalf("workflow runtime profile = %q, want local-node", workflow.RuntimeProfileName)
	}
	if workflow.RoutePath != "/workflows/slack-clone-runner/run" || workflow.RouteAuth != "workspace" {
		t.Fatalf("workflow route = %q auth=%q, want active route binding", workflow.RoutePath, workflow.RouteAuth)
	}
	if workflow.TriggerEvent != "issue.label_added" || workflow.TriggerFilter["type"] != "epic" {
		t.Fatalf("workflow trigger = %q filter=%+v, want active trigger binding", workflow.TriggerEvent, workflow.TriggerFilter)
	}
	runtime := plan.Runtimes[0]
	if runtime.Name != "local-node" || runtime.Provider != domain.RuntimeProviderLocal || runtime.Image != "node:22" {
		t.Fatalf("runtime = %+v, want direct runtime profile projection", runtime)
	}
	if plan.Tools[0].Name != "github_issue_read" || plan.Tools[0].Handler != "workflow" {
		t.Fatalf("tools = %+v, want active tool DefinitionVersion projection", plan.Tools)
	}
	run := plan.WorkflowRuns[0]
	if run.RunID != "wrun-slack-1" || run.WorkflowName != "slack-clone-runner" ||
		run.WorkflowVersion != "control-v1" || run.BundleHash != "workflow-bundle-hash" ||
		run.IdempotencyKey != "slack-clone-runner:EPIC-1" {
		t.Fatalf("workflow run = %+v, want durable workflow run identity", run)
	}
	if run.Status != domain.WorkflowRunWaiting || run.WaitCondition != waitCondition ||
		run.FencingToken != fencingToken || run.StartedAt == nil || !run.StartedAt.Equal(startedAt) ||
		string(run.Input) != `{"parentId":"EPIC-1"}` || string(run.Result) != `{"ready":2}` {
		t.Fatalf("workflow run state = %+v, want round-trippable durable state", run)
	}
	task := plan.TaskRuns[0]
	if task.TaskRunID != "trun-slack-1" || task.WorkflowRunID != "wrun-slack-1" ||
		task.WorkItemID != "TASK-1" || task.RoleName != "task" ||
		task.Status != domain.TaskRunRunning || task.AgentID != "triage-bot" ||
		task.CommandID != "cmd-1" || task.SessionID != "session-1" ||
		task.ClaimActor != "triage-bot" || task.ClaimEventID != "claim-event-1" ||
		task.Metadata["surface"] != "sidebar" || task.StartedAt == nil || !task.StartedAt.Equal(taskStartedAt) {
		t.Fatalf("task run = %+v, want durable task run runtime state", task)
	}
	event := plan.RunEvents[0]
	if event.EventID != "rev-slack-1" || event.WorkflowRunID != "wrun-slack-1" ||
		event.TaskRunID != "trun-slack-1" || event.Type != "task_run_dispatched" ||
		event.Message != "task run dispatched" || event.EventIndex != 1 ||
		string(event.Data) != `{"command_id":"cmd-1","task_run_id":"trun-slack-1"}` {
		t.Fatalf("run event = %+v, want durable run-event audit state", event)
	}

	imported := memstore.New()
	if _, err := imported.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "IMPORT", Name: "Imported"}); err != nil {
		t.Fatalf("create import workspace: %v", err)
	}
	if err := Apply(ctx, imported, "IMPORT", "test", plan); err != nil {
		t.Fatalf("Apply(imported plan) error = %v", err)
	}
	importedAgent, err := imported.Agents().Get(ctx, "IMPORT", "triage-bot")
	if err != nil {
		t.Fatalf("get imported agent instance: %v", err)
	}
	if importedAgent.RoleName != "triage" || importedAgent.DesiredState != domain.AgentDesiredRunning ||
		importedAgent.State != domain.AgentStateActive || importedAgent.TaskFilter != "needs_design" {
		t.Fatalf("imported agent = %+v, want round-tripped durable agent instance", importedAgent)
	}
	importedLease, err := imported.AgentLeases().Get(ctx, "IMPORT", "lease-task-1")
	if err != nil {
		t.Fatalf("get imported agent lease: %v", err)
	}
	if importedLease.SessionID != "session-1" || importedLease.AgentID != "triage-bot" ||
		importedLease.Token != "task-token-1" || importedLease.FencingToken != 11 ||
		importedLease.Status != domain.AgentLeaseActive ||
		!importedLease.ExpiresAt.Equal(leaseExpiresAt) || !importedLease.LastHeartbeat.Equal(sessionHeartbeat) {
		t.Fatalf("imported agent lease = %+v, want round-tripped durable lease state", importedLease)
	}
	importedOwnership, err := imported.AgentOwnershipLeases().Get(ctx, "IMPORT", "triage-bot")
	if err != nil {
		t.Fatalf("get imported agent ownership lease: %v", err)
	}
	if importedOwnership.LeaseID != "owner-lease-1" || importedOwnership.OwnerID != "daemon-local" ||
		importedOwnership.Token != "owner-token-1" || importedOwnership.FencingToken != 13 ||
		importedOwnership.Status != domain.AgentLeaseActive ||
		!importedOwnership.ExpiresAt.Equal(ownershipExpiresAt) ||
		!importedOwnership.LastHeartbeat.Equal(sessionHeartbeat) {
		t.Fatalf("imported ownership lease = %+v, want round-tripped durable ownership state", importedOwnership)
	}
	importedWorkflow, err := imported.WorkflowDefinitions().Get(ctx, "IMPORT", "slack-clone-runner")
	if err != nil {
		t.Fatalf("get imported workflow definition: %v", err)
	}
	if importedWorkflow.RuntimeProfileName != "local-node" {
		t.Fatalf("imported workflow runtime profile = %q, want local-node", importedWorkflow.RuntimeProfileName)
	}
	importedRun, err := imported.WorkflowRuns().Get(ctx, "IMPORT", "wrun-slack-1")
	if err != nil {
		t.Fatalf("get imported workflow run: %v", err)
	}
	if importedRun.WorkflowName != "slack-clone-runner" || importedRun.Status != domain.WorkflowRunWaiting ||
		importedRun.WaitCondition != waitCondition || importedRun.FencingToken != fencingToken ||
		importedRun.LeaseOwner != "epic-runner" || importedRun.LeaseToken != "lease-1" ||
		!importedRun.StartedAt.Equal(startedAt) || string(importedRun.Input) != `{"parentId":"EPIC-1"}` ||
		string(importedRun.Result) != `{"ready":2}` {
		t.Fatalf("imported workflow run = %+v, want round-tripped durable run state", importedRun)
	}
	importedTaskRun, err := imported.TaskRuns().Get(ctx, "IMPORT", "trun-slack-1")
	if err != nil {
		t.Fatalf("get imported task run: %v", err)
	}
	if importedTaskRun.WorkflowRunID != "wrun-slack-1" || importedTaskRun.WorkItemID != "TASK-1" ||
		importedTaskRun.Status != domain.TaskRunRunning || importedTaskRun.AgentID != "triage-bot" ||
		importedTaskRun.CommandID != "cmd-1" || importedTaskRun.SessionID != "session-1" ||
		importedTaskRun.ClaimActor != "triage-bot" || importedTaskRun.ClaimEventID != "claim-event-1" ||
		importedTaskRun.Metadata["app"] != "slack" || !importedTaskRun.StartedAt.Equal(taskStartedAt) {
		t.Fatalf("imported task run = %+v, want round-tripped durable task run state", importedTaskRun)
	}
	importedEvents, err := imported.RunEvents().List(ctx, "IMPORT", store.RunEventFilter{WorkflowRunID: "wrun-slack-1"})
	if err != nil {
		t.Fatalf("list imported run events: %v", err)
	}
	if len(importedEvents) != 1 || importedEvents[0].EventID != "rev-slack-1" ||
		importedEvents[0].TaskRunID != "trun-slack-1" ||
		importedEvents[0].Type != "task_run_dispatched" ||
		string(importedEvents[0].Data) != `{"command_id":"cmd-1","task_run_id":"trun-slack-1"}` {
		t.Fatalf("imported run events = %+v, want round-tripped run-event state", importedEvents)
	}
	if err := Apply(ctx, imported, "IMPORT", "test", plan); err != nil {
		t.Fatalf("Apply(imported plan again) error = %v", err)
	}
	importedEvents, err = imported.RunEvents().List(ctx, "IMPORT", store.RunEventFilter{WorkflowRunID: "wrun-slack-1"})
	if err != nil {
		t.Fatalf("list imported run events after reapply: %v", err)
	}
	if len(importedEvents) != 1 {
		t.Fatalf("imported run events after reapply = %+v, want idempotent run-event import", importedEvents)
	}
	importedSession, err := imported.AgentSessions().Get(ctx, "IMPORT", "session-1")
	if err != nil {
		t.Fatalf("get imported agent session: %v", err)
	}
	if importedSession.AgentID != "triage-bot" || importedSession.NodeID != "node-local" ||
		importedSession.Kind != domain.AgentSessionKindTask || importedSession.TaskID != "TASK-1" ||
		importedSession.TerminalID != "term-1" || importedSession.ParentSessionID != "session-parent" ||
		importedSession.Status != domain.AgentSessionRunning || importedSession.Phase != "implementing" ||
		importedSession.Attempt != 2 || importedSession.Summary != "working on sidebar" ||
		importedSession.Metadata["workflow_run_id"] != "wrun-slack-1" ||
		!importedSession.LastHeartbeat.Equal(sessionHeartbeat) {
		t.Fatalf("imported agent session = %+v, want round-tripped durable session state", importedSession)
	}
	importedCommand, err := imported.AgentCommands().Get(ctx, "IMPORT", "cmd-1")
	if err != nil {
		t.Fatalf("get imported agent command: %v", err)
	}
	if importedCommand.TargetAgentID != "triage-bot" || importedCommand.TargetNodeID != "node-local" ||
		importedCommand.SessionID != "session-1" || importedCommand.Type != "start-task" ||
		importedCommand.Status != domain.AgentCommandSucceeded || importedCommand.Result != "started" ||
		importedCommand.Payload["workflow_run_id"] != "wrun-slack-1" {
		t.Fatalf("imported agent command = %+v, want round-tripped command state", importedCommand)
	}
	importedTerminal, err := imported.TerminalSessions().Get(ctx, "IMPORT", "term-1")
	if err != nil {
		t.Fatalf("get imported terminal session: %v", err)
	}
	if importedTerminal.AgentID != "triage-bot" || importedTerminal.SessionID != "session-1" ||
		importedTerminal.NodeID != "node-local" || importedTerminal.TaskID != "TASK-1" ||
		importedTerminal.Title != "Slack runner terminal" || importedTerminal.Kind != "pty" ||
		importedTerminal.Status != domain.TerminalSessionOpen || importedTerminal.PTYProvider != "local" ||
		importedTerminal.StreamRef != "stream://term-1" || importedTerminal.TranscriptRef != "transcript://term-1" ||
		importedTerminal.AttachedClients != 1 || importedTerminal.Metadata["pane"] != "runner" ||
		!importedTerminal.LastSeenAt.Equal(terminalSeenAt) {
		t.Fatalf("imported terminal session = %+v, want round-tripped terminal state", importedTerminal)
	}
	importedArtifact, err := imported.Artifacts().Get(ctx, "IMPORT", "artifact-slack-1")
	if err != nil {
		t.Fatalf("get imported artifact: %v", err)
	}
	if importedArtifact.AgentID != "triage-bot" || importedArtifact.SessionID != "session-1" ||
		importedArtifact.TerminalID != "term-1" || importedArtifact.TaskID != "TASK-1" ||
		importedArtifact.Type != "report" || importedArtifact.URI != "artifact://workflow/wrun-slack-1/report.json" ||
		importedArtifact.Summary != "Slack runner report" || importedArtifact.MIMEType != "application/json" ||
		importedArtifact.SizeBytes != 512 || importedArtifact.Checksum != "sha256:abc123" ||
		importedArtifact.Metadata["workflow_run_id"] != "wrun-slack-1" {
		t.Fatalf("imported artifact = %+v, want round-tripped artifact state", importedArtifact)
	}
}

func TestPlanFromWorkspacePrefersSourceBackedDefinitions(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "SRC", Name: "Source Backed"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	writeDefFile(t, root, ".loom/agents/triage.ts", `export default defineAgent({
  name: "triage",
  backend: "echo",
  model: "local/echo",
  tools: ["github.issue.read"],
  allowedCommands: ["npm test"],
});`)
	sourcePlan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := Apply(ctx, st, "SRC", "test", sourcePlan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	workspacePlan, err := PlanFromWorkspace(ctx, st, "SRC")
	if err != nil {
		t.Fatalf("PlanFromWorkspace() error = %v", err)
	}
	if len(workspacePlan.Agents) != 1 {
		t.Fatalf("agents = %+v, want one source-backed agent without duplicate role projection", workspacePlan.Agents)
	}
	agent := workspacePlan.Agents[0]
	if agent.SourcePath != sourcePlan.Agents[0].SourcePath || agent.Version != sourcePlan.Agents[0].Version {
		t.Fatalf("agent = %+v, want source-backed definition %+v", agent, sourcePlan.Agents[0])
	}
	if len(agent.AllowedCommands) != 1 || agent.AllowedCommands[0] != "npm test" {
		t.Fatalf("allowed commands = %+v, want source manifest command policy", agent.AllowedCommands)
	}
}

func TestPlanFromWorkspaceDecodesRuntimeBuiltinWorkflowManifest(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BUILTIN", Name: "Runtime Builtin"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.WorkflowDefinitions().Upsert(ctx, store.WorkflowDefinitionUpsert{
		WorkspaceKey:    "BUILTIN",
		Name:            "run-parent-work-items",
		Version:         "builtin-v1",
		Description:     "Ensure one live TaskRun per ready child.",
		SingletonPolicy: "parent:${input.parentId}",
		SourceRef:       "builtin:run-parent-work-items",
		BundleHash:      "builtin:run-parent-work-items:v1",
		Manifest:        json.RawMessage(`{"builtin":true,"runner":"go","kind":"parent-work-items"}`),
		Status:          domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert workflow definition: %v", err)
	}

	plan, err := PlanFromWorkspace(ctx, st, "BUILTIN")
	if err != nil {
		t.Fatalf("PlanFromWorkspace() error = %v", err)
	}
	if len(plan.Workflows) != 1 {
		t.Fatalf("workflows = %+v, want one runtime builtin workflow", plan.Workflows)
	}
	workflow := plan.Workflows[0]
	if workflow.Name != "run-parent-work-items" || workflow.Runner != "go" || workflow.Builtin != "" {
		t.Fatalf("workflow = %+v, want runtime builtin manifest decoded without delegated builtin", workflow)
	}
	if workflow.SourcePath != "builtin:run-parent-work-items" || workflow.SourceHash != "builtin:run-parent-work-items:v1" {
		t.Fatalf("workflow source = %q hash=%q, want runtime builtin source evidence", workflow.SourcePath, workflow.SourceHash)
	}
}
