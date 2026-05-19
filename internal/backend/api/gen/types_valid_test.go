package gen

import "testing"

type validEnum interface {
	Valid() bool
}

func assertValidEnumValues[T validEnum](t *testing.T, values []T, invalid T) {
	t.Helper()
	for _, value := range values {
		if !value.Valid() {
			t.Fatalf("%T(%v).Valid() = false, want true", value, value)
		}
	}
	if invalid.Valid() {
		t.Fatalf("%T(%v).Valid() = true, want false", invalid, invalid)
	}
}

func TestGeneratedEnumValidMethods(t *testing.T) {
	assertValidEnumValues(t, []AgentStatusResponseAgentState{
		AgentStatusResponseAgentStateDead,
		AgentStatusResponseAgentStateDone,
		AgentStatusResponseAgentStateIdle,
		AgentStatusResponseAgentStateRunning,
		AgentStatusResponseAgentStateSpawning,
		AgentStatusResponseAgentStateStopped,
		AgentStatusResponseAgentStateStuck,
		AgentStatusResponseAgentStateWorking,
	}, AgentStatusResponseAgentState("invalid"))
	assertValidEnumValues(t, []BlockedIssueAgentState{
		BlockedIssueAgentStateDead,
		BlockedIssueAgentStateDone,
		BlockedIssueAgentStateIdle,
		BlockedIssueAgentStateRunning,
		BlockedIssueAgentStateSpawning,
		BlockedIssueAgentStateStopped,
		BlockedIssueAgentStateStuck,
		BlockedIssueAgentStateWorking,
	}, BlockedIssueAgentState("invalid"))
	assertValidEnumValues(t, []BlockedIssueIssueType{
		BlockedIssueIssueTypeBug,
		BlockedIssueIssueTypeChore,
		BlockedIssueIssueTypeEpic,
		BlockedIssueIssueTypeFeature,
		BlockedIssueIssueTypeTask,
	}, BlockedIssueIssueType("invalid"))
	assertValidEnumValues(t, []BlockedIssueStatus{
		BlockedIssueStatusBlocked,
		BlockedIssueStatusClosed,
		BlockedIssueStatusDeferred,
		BlockedIssueStatusInProgress,
		BlockedIssueStatusOpen,
		BlockedIssueStatusReview,
	}, BlockedIssueStatus("invalid"))
	assertValidEnumValues(t, []CreateIssueRequestIssueType{
		CreateIssueRequestIssueTypeBug,
		CreateIssueRequestIssueTypeChore,
		CreateIssueRequestIssueTypeEpic,
		CreateIssueRequestIssueTypeFeature,
		CreateIssueRequestIssueTypeTask,
	}, CreateIssueRequestIssueType("invalid"))
	assertValidEnumValues(t, []CreateIssueRequestStatus{
		CreateIssueRequestStatusDeferred,
		CreateIssueRequestStatusOpen,
	}, CreateIssueRequestStatus("invalid"))
	assertValidEnumValues(t, []ErrorResponseSuccess{False}, ErrorResponseSuccess(true))
	assertValidEnumValues(t, []IssueAgentState{
		IssueAgentStateDead,
		IssueAgentStateDone,
		IssueAgentStateIdle,
		IssueAgentStateRunning,
		IssueAgentStateSpawning,
		IssueAgentStateStopped,
		IssueAgentStateStuck,
		IssueAgentStateWorking,
	}, IssueAgentState("invalid"))
	assertValidEnumValues(t, []IssueIssueType{
		IssueIssueTypeBug,
		IssueIssueTypeChore,
		IssueIssueTypeEpic,
		IssueIssueTypeFeature,
		IssueIssueTypeTask,
	}, IssueIssueType("invalid"))
	assertValidEnumValues(t, []IssueStatus{
		IssueStatusBlocked,
		IssueStatusClosed,
		IssueStatusDeferred,
		IssueStatusInProgress,
		IssueStatusOpen,
		IssueStatusReview,
	}, IssueStatus("invalid"))
	assertValidEnumValues(t, []IssueResponseIssueType{
		IssueResponseIssueTypeBug,
		IssueResponseIssueTypeChore,
		IssueResponseIssueTypeEpic,
		IssueResponseIssueTypeFeature,
		IssueResponseIssueTypeTask,
	}, IssueResponseIssueType("invalid"))
	assertValidEnumValues(t, []IssueResponseStatus{
		IssueResponseStatusBlocked,
		IssueResponseStatusClosed,
		IssueResponseStatusDeferred,
		IssueResponseStatusInProgress,
		IssueResponseStatusOpen,
		IssueResponseStatusReview,
	}, IssueResponseStatus("invalid"))
	assertValidEnumValues(t, []IssueTabType{Details, Logs, Sessions, Terminal}, IssueTabType("invalid"))
	assertValidEnumValues(t, []MessageResponseSuccess{True}, MessageResponseSuccess(false))
	assertValidEnumValues(t, []MonitorWorkspaceInfoMode{MonitorWorkspaceInfoModeWorkspace}, MonitorWorkspaceInfoMode("invalid"))
	assertValidEnumValues(t, []MonitorWorkspacesResponseMode{MonitorWorkspacesResponseModeWorkspace}, MonitorWorkspacesResponseMode("invalid"))
	assertValidEnumValues(t, []MutationPayloadType{
		MutationPayloadTypeBonded,
		MutationPayloadTypeBurned,
		MutationPayloadTypeComment,
		MutationPayloadTypeCreate,
		MutationPayloadTypeDelete,
		MutationPayloadTypeIssueTabs,
		MutationPayloadTypeRefresh,
		MutationPayloadTypeSessionChange,
		MutationPayloadTypeSquashed,
		MutationPayloadTypeStatus,
		MutationPayloadTypeTerminalMetadata,
		MutationPayloadTypeTerminalSessionChange,
		MutationPayloadTypeUpdate,
	}, MutationPayloadType("invalid"))
	assertValidEnumValues(t, []ObservabilityEventType{
		AgentRestarted,
		AgentStarted,
		AgentStopped,
		ConflictResolved,
		EpicAssigned,
		EpicExhausted,
		PrCreated,
		SystemConfigReloaded,
		SystemHealthCheck,
		TaskClaimed,
		TaskCompleted,
		TaskFailed,
		TaskStarted,
	}, ObservabilityEventType("invalid"))
	assertValidEnumValues(t, []PatchIssueRequestAgentState{
		PatchIssueRequestAgentStateDead,
		PatchIssueRequestAgentStateDone,
		PatchIssueRequestAgentStateIdle,
		PatchIssueRequestAgentStateRunning,
		PatchIssueRequestAgentStateSpawning,
		PatchIssueRequestAgentStateStopped,
		PatchIssueRequestAgentStateStuck,
		PatchIssueRequestAgentStateWorking,
	}, PatchIssueRequestAgentState("invalid"))
	assertValidEnumValues(t, []PatchIssueRequestStatus{
		PatchIssueRequestStatusBlocked,
		PatchIssueRequestStatusClosed,
		PatchIssueRequestStatusDeferred,
		PatchIssueRequestStatusInProgress,
		PatchIssueRequestStatusOpen,
		PatchIssueRequestStatusReview,
	}, PatchIssueRequestStatus("invalid"))
	assertValidEnumValues(t, []SessionHistoryRecordLauncher{SessionHistoryRecordLauncherStartWork, SessionHistoryRecordLauncherUser}, SessionHistoryRecordLauncher("invalid"))
	assertValidEnumValues(t, []SessionHistoryRecordStatus{Active, Completed}, SessionHistoryRecordStatus("invalid"))
	assertValidEnumValues(t, []TranscriptEntryRole{TranscriptEntryRoleAssistant, TranscriptEntryRoleSystem, TranscriptEntryRoleTool, TranscriptEntryRoleUser}, TranscriptEntryRole("invalid"))
	assertValidEnumValues(t, []TranscriptEntryType{Text, ToolResult, ToolUse}, TranscriptEntryType("invalid"))
	assertValidEnumValues(t, []TreeNodeAgentState{
		Dead,
		Done,
		Idle,
		Running,
		Spawning,
		Stopped,
		Stuck,
		Working,
	}, TreeNodeAgentState("invalid"))
	assertValidEnumValues(t, []TreeNodeIssueType{
		TreeNodeIssueTypeBug,
		TreeNodeIssueTypeChore,
		TreeNodeIssueTypeEpic,
		TreeNodeIssueTypeFeature,
		TreeNodeIssueTypeTask,
	}, TreeNodeIssueType("invalid"))
	assertValidEnumValues(t, []TreeNodeStatus{
		TreeNodeStatusBlocked,
		TreeNodeStatusClosed,
		TreeNodeStatusDeferred,
		TreeNodeStatusInProgress,
		TreeNodeStatusOpen,
		TreeNodeStatusReview,
	}, TreeNodeStatus("invalid"))
	assertValidEnumValues(t, []ListBlockedParamsType{
		ListBlockedParamsTypeBug,
		ListBlockedParamsTypeChore,
		ListBlockedParamsTypeEpic,
		ListBlockedParamsTypeFeature,
		ListBlockedParamsTypeTask,
	}, ListBlockedParamsType("invalid"))
	assertValidEnumValues(t, []ListIssuesParamsStatus{
		ListIssuesParamsStatusBlocked,
		ListIssuesParamsStatusClosed,
		ListIssuesParamsStatusDeferred,
		ListIssuesParamsStatusInProgress,
		ListIssuesParamsStatusOpen,
		ListIssuesParamsStatusReview,
	}, ListIssuesParamsStatus("invalid"))
	assertValidEnumValues(t, []ListIssuesParamsType{
		ListIssuesParamsTypeBug,
		ListIssuesParamsTypeChore,
		ListIssuesParamsTypeEpic,
		ListIssuesParamsTypeFeature,
		ListIssuesParamsTypeTask,
	}, ListIssuesParamsType("invalid"))
	assertValidEnumValues(t, []GetGraphParamsStatus{GetGraphParamsStatusAll, GetGraphParamsStatusClosed, GetGraphParamsStatusOpen}, GetGraphParamsStatus("invalid"))
	assertValidEnumValues(t, []ListReadyParamsType{
		ListReadyParamsTypeBug,
		ListReadyParamsTypeChore,
		ListReadyParamsTypeEpic,
		ListReadyParamsTypeFeature,
		ListReadyParamsTypeTask,
	}, ListReadyParamsType("invalid"))
	assertValidEnumValues(t, []ListReadyParamsMolType{Patrol, Swarm, Work}, ListReadyParamsMolType("invalid"))
	assertValidEnumValues(t, []ListReadyParamsSort{Hybrid, Oldest, Priority}, ListReadyParamsSort("invalid"))
	assertValidEnumValues(t, []StartTerminalSetupJSONBodyAction{Configure, Install, Login, Test}, StartTerminalSetupJSONBodyAction("invalid"))
}
