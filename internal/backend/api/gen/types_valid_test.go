package gen

import "testing"

type validatableEnum interface {
	Valid() bool
}

func TestGeneratedEnumValidMethods(t *testing.T) {
	tests := []struct {
		name    string
		valid   validatableEnum
		invalid validatableEnum
	}{
		{"AgentStatusResponseAgentState", AgentStatusResponseAgentStateDead, AgentStatusResponseAgentState("invalid")},
		{"BlockedIssueAgentState", BlockedIssueAgentStateDead, BlockedIssueAgentState("invalid")},
		{"BlockedIssueIssueType", BlockedIssueIssueTypeBug, BlockedIssueIssueType("invalid")},
		{"BlockedIssueStatus", BlockedIssueStatusOpen, BlockedIssueStatus("invalid")},
		{"CreateIssueRequestIssueType", CreateIssueRequestIssueTypeTask, CreateIssueRequestIssueType("invalid")},
		{"ErrorResponseSuccess", False, ErrorResponseSuccess(true)},
		{"IssueAgentState", IssueAgentStateIdle, IssueAgentState("invalid")},
		{"IssueIssueType", IssueIssueTypeFeature, IssueIssueType("invalid")},
		{"IssueStatus", IssueStatusReview, IssueStatus("invalid")},
		{"IssueResponseIssueType", IssueResponseIssueTypeChore, IssueResponseIssueType("invalid")},
		{"IssueResponseStatus", IssueResponseStatusBlocked, IssueResponseStatus("invalid")},
		{"IssueTabType", Sessions, IssueTabType("invalid")},
		{"MessageResponseSuccess", True, MessageResponseSuccess(false)},
		{"MonitorWorkspaceInfoMode", MonitorWorkspaceInfoModeWorkspace, MonitorWorkspaceInfoMode("invalid")},
		{"MonitorWorkspacesResponseMode", MonitorWorkspacesResponseModeWorkspace, MonitorWorkspacesResponseMode("invalid")},
		{"MutationPayloadType", MutationPayloadTypeCreate, MutationPayloadType("invalid")},
		{"ObservabilityEventType", AgentStarted, ObservabilityEventType("invalid")},
		{"PatchIssueRequestAgentState", PatchIssueRequestAgentStateWorking, PatchIssueRequestAgentState("invalid")},
		{"PatchIssueRequestStatus", PatchIssueRequestStatusInProgress, PatchIssueRequestStatus("invalid")},
		{"TranscriptEntryRole", Assistant, TranscriptEntryRole("invalid")},
		{"TranscriptEntryType", Text, TranscriptEntryType("invalid")},
		{"TreeNodeAgentState", Running, TreeNodeAgentState("invalid")},
		{"TreeNodeIssueType", TreeNodeIssueTypeEpic, TreeNodeIssueType("invalid")},
		{"TreeNodeStatus", TreeNodeStatusClosed, TreeNodeStatus("invalid")},
		{"ListBlockedParamsType", ListBlockedParamsTypeTask, ListBlockedParamsType("invalid")},
		{"ListIssuesParamsStatus", ListIssuesParamsStatusOpen, ListIssuesParamsStatus("invalid")},
		{"ListIssuesParamsType", ListIssuesParamsTypeBug, ListIssuesParamsType("invalid")},
		{"GetGraphParamsStatus", GetGraphParamsStatusAll, GetGraphParamsStatus("invalid")},
		{"ListReadyParamsType", ListReadyParamsTypeFeature, ListReadyParamsType("invalid")},
		{"ListReadyParamsMolType", Work, ListReadyParamsMolType("invalid")},
		{"ListReadyParamsSort", Priority, ListReadyParamsSort("invalid")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.valid.Valid() {
				t.Fatalf("%s valid value returned false", tt.name)
			}
			if tt.invalid.Valid() {
				t.Fatalf("%s invalid value returned true", tt.name)
			}
		})
	}
}
