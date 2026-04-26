package backend

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Section 1: IssueData JSON round-trip
// ---------------------------------------------------------------------------

func TestIssueData_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	due := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	original := IssueData{
		ID:              "issue-1",
		Title:           "Fix the widget",
		Status:          "open",
		Priority:        2,
		IssueType:       "task",
		Assignee:        "alice",
		Owner:           "bob",
		Labels:          []string{"bug", "urgent"},
		SourceRepo:      "main-repo",
		Parent:          "epic-1",
		CreatedAt:       now,
		UpdatedAt:       now.Add(time.Hour),
		DueAt:           &due,
		DependencyCount: 3,
		DependentCount:  1,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded IssueData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, original.ID)
	}
	if decoded.Title != original.Title {
		t.Errorf("Title = %q, want %q", decoded.Title, original.Title)
	}
	if decoded.Priority != original.Priority {
		t.Errorf("Priority = %d, want %d", decoded.Priority, original.Priority)
	}
	if decoded.DueAt == nil || !decoded.DueAt.Equal(due) {
		t.Errorf("DueAt = %v, want %v", decoded.DueAt, due)
	}
	if decoded.DeferUntil != nil {
		t.Errorf("DeferUntil = %v, want nil", decoded.DeferUntil)
	}
	if decoded.DependencyCount != original.DependencyCount {
		t.Errorf("DependencyCount = %d, want %d", decoded.DependencyCount, original.DependencyCount)
	}
	if !decoded.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", decoded.CreatedAt, original.CreatedAt)
	}
}

// ---------------------------------------------------------------------------
// Section 2: Priority zero-value must serialize (no omitempty)
// ---------------------------------------------------------------------------

func TestIssueData_PriorityZeroValueSerializes(t *testing.T) {
	issue := IssueData{
		ID:        "p0-issue",
		Title:     "Critical",
		Status:    "open",
		Priority:  0, // P0 — must appear in JSON
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	data, err := json.Marshal(issue)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	raw := string(data)
	if !strings.Contains(raw, `"priority":0`) {
		t.Errorf("Priority 0 must be present in JSON output, got: %s", raw)
	}
}

func TestCreateParams_PriorityZeroValueSerializes(t *testing.T) {
	params := CreateParams{
		Title:     "Critical bug",
		IssueType: "task",
		Priority:  0,
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	raw := string(data)
	if !strings.Contains(raw, `"priority":0`) {
		t.Errorf("Priority 0 must be present in JSON output, got: %s", raw)
	}
}

// ---------------------------------------------------------------------------
// Section 3: IssueDetailData embedding
// ---------------------------------------------------------------------------

func TestIssueDetailData_EmbeddedFieldsAccessible(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	detail := IssueDetailData{
		IssueData: IssueData{
			ID:        "detail-1",
			Title:     "Embedded test",
			Status:    "closed",
			Priority:  1,
			Assignee:  "carol",
			CreatedAt: now,
			UpdatedAt: now.Add(time.Hour),
		},
		Description: "A long description",
	}

	// Verify embedded fields are accessible directly.
	if detail.ID != "detail-1" {
		t.Errorf("ID via embedding = %q, want %q", detail.ID, "detail-1")
	}
	if detail.Title != "Embedded test" {
		t.Errorf("Title via embedding = %q, want %q", detail.Title, "Embedded test")
	}
	if detail.Priority != 1 {
		t.Errorf("Priority via embedding = %d, want %d", detail.Priority, 1)
	}
	// Verify explicit field on IssueDetailData.
	if detail.Description != "A long description" {
		t.Errorf("Description = %q, want %q", detail.Description, "A long description")
	}
}

func TestIssueDetailData_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	closedAt := now.Add(5 * time.Hour)
	estMins := 45

	original := IssueDetailData{
		IssueData: IssueData{
			ID:          "rt-1",
			Title:       "Round trip detail",
			Status:      "done",
			Priority:    3,
			IssueType:   "epic",
			Labels:      []string{"backend"},
			Design:      "Round trip design",
			CreatedAt:   now,
			UpdatedAt:   now.Add(time.Hour),
			CreatedBy:   "frank",
			ClosedAt:    &closedAt,
			CloseReason: "completed",
		},
		Description:      "Full description",
		ExternalRef:      "GH-42",
		EstimatedMinutes: &estMins,
		Dependencies: []DependencyData{
			{IssueID: "rt-1", DependsOnID: "blocker-1", Type: "blocks", Title: "Blocker", Status: "open", CreatedAt: now},
		},
		Dependents: []DependencyData{
			{IssueID: "child-1", DependsOnID: "rt-1", Type: "blocks", CreatedAt: now},
		},
		Comments: []CommentData{
			{ID: 10, IssueID: "rt-1", Author: "grace", Text: "Nice work", CreatedAt: now},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded IssueDetailData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Check embedded IssueData fields.
	if decoded.ID != original.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, original.ID)
	}
	if decoded.Priority != original.Priority {
		t.Errorf("Priority = %d, want %d", decoded.Priority, original.Priority)
	}
	if len(decoded.Labels) != 1 || decoded.Labels[0] != "backend" {
		t.Errorf("Labels = %v, want [backend]", decoded.Labels)
	}

	// Check Design survives round-trip (promoted from IssueData).
	if !strings.Contains(string(data), `"design":"Round trip design"`) {
		t.Errorf("marshaled JSON should contain design value, got: %s", data)
	}
	if decoded.Design != "Round trip design" {
		t.Errorf("Design = %q, want %q", decoded.Design, "Round trip design")
	}

	// Check IssueDetailData-specific fields.
	if decoded.Description != original.Description {
		t.Errorf("Description = %q, want %q", decoded.Description, original.Description)
	}
	if decoded.ClosedAt == nil || !decoded.ClosedAt.Equal(closedAt) {
		t.Errorf("ClosedAt = %v, want %v", decoded.ClosedAt, closedAt)
	}
	if decoded.EstimatedMinutes == nil || *decoded.EstimatedMinutes != estMins {
		t.Errorf("EstimatedMinutes = %v, want %d", decoded.EstimatedMinutes, estMins)
	}
	if len(decoded.Dependencies) != 1 {
		t.Fatalf("Dependencies len = %d, want 1", len(decoded.Dependencies))
	}
	if decoded.Dependencies[0].DependsOnID != "blocker-1" {
		t.Errorf("Dependencies[0].DependsOnID = %q, want %q", decoded.Dependencies[0].DependsOnID, "blocker-1")
	}
	if len(decoded.Comments) != 1 || decoded.Comments[0].Author != "grace" {
		t.Errorf("Comments = %v, want [{Author: grace}]", decoded.Comments)
	}
}

// ---------------------------------------------------------------------------
// Section 3b: Regression — Design must not be shadowed by IssueDetailData
// ---------------------------------------------------------------------------

func TestIssueDetailData_DesignNotShadowed(t *testing.T) {
	detail := IssueDetailData{
		IssueData: IssueData{
			ID:     "shadow-1",
			Title:  "Shadow test",
			Status: "open",
			Design: "my design",
		},
	}

	// Marshal should include the design value.
	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	raw := string(data)
	if !strings.Contains(raw, `"design":"my design"`) {
		t.Errorf("JSON should contain design value, got: %s", raw)
	}

	// Unmarshal back and verify Design survives.
	var decoded IssueDetailData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Design != "my design" {
		t.Errorf("Design = %q, want %q", decoded.Design, "my design")
	}

	// Verify dot-access resolves to the promoted field.
	if decoded.IssueData.Design != "my design" {
		t.Errorf("IssueData.Design = %q, want %q", decoded.IssueData.Design, "my design")
	}
}

// ---------------------------------------------------------------------------
// Section 4: StatsData JSON round-trip
// ---------------------------------------------------------------------------

func TestStatsData_JSONRoundTrip(t *testing.T) {
	original := StatsData{
		TotalIssues:             100,
		OpenIssues:              40,
		InProgressIssues:        15,
		ClosedIssues:            30,
		BlockedIssues:           5,
		DeferredIssues:          3,
		ReadyIssues:             7,
		TombstoneIssues:         0,
		PinnedIssues:            2,
		EpicsEligibleForClosure: 1,
		AverageLeadTime:         48.5,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded StatsData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded != original {
		t.Errorf("StatsData round-trip mismatch:\n got  %+v\n want %+v", decoded, original)
	}
}

func TestStatsData_ZeroValuesPresent(t *testing.T) {
	stats := StatsData{
		TotalIssues: 10,
		OpenIssues:  5,
		// AverageLeadTime is zero — no omitempty, so it must be present (0 is meaningful).
	}

	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	raw := string(data)
	if !strings.Contains(raw, `"average_lead_time_hours":0`) {
		t.Errorf("AverageLeadTime 0 must be present (no omitempty), got: %s", raw)
	}
	if !strings.Contains(raw, `"total_issues":10`) {
		t.Errorf("total_issues should be present, got: %s", raw)
	}
	if !strings.Contains(raw, `"epics_eligible_for_closure":0`) {
		t.Errorf("EpicsEligibleForClosure 0 must be present, got: %s", raw)
	}
}

// ---------------------------------------------------------------------------
// Section 5: MutationData JSON round-trip
// ---------------------------------------------------------------------------

func TestMutationData_JSONRoundTrip(t *testing.T) {
	ts := time.Date(2026, 4, 1, 8, 30, 0, 0, time.UTC)
	original := MutationData{
		Type:      MutationStatus,
		IssueID:   "mut-1",
		Title:     "Status change",
		Assignee:  "heidi",
		Actor:     "ivan",
		Timestamp: ts,
		OldStatus: "open",
		NewStatus: "in_progress",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded MutationData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Type != original.Type {
		t.Errorf("Type = %q, want %q", decoded.Type, original.Type)
	}
	if decoded.OldStatus != original.OldStatus {
		t.Errorf("OldStatus = %q, want %q", decoded.OldStatus, original.OldStatus)
	}
	if decoded.NewStatus != original.NewStatus {
		t.Errorf("NewStatus = %q, want %q", decoded.NewStatus, original.NewStatus)
	}
	if !decoded.Timestamp.Equal(original.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", decoded.Timestamp, original.Timestamp)
	}
}

// ---------------------------------------------------------------------------
// Section 6: UpdateParams pointer fields — nil vs zero-value
// ---------------------------------------------------------------------------

func TestUpdateParams_NilFieldsOmitted(t *testing.T) {
	params := UpdateParams{} // All pointer fields nil.

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	raw := string(data)
	// All pointer fields should be omitted.
	for _, field := range []string{
		"title", "description", "status", "priority", "design",
		"acceptance_criteria", "notes", "assignee", "owner",
		"issue_type", "external_ref", "estimated_minutes",
		"parent", "agent_state", "due_at", "defer_until",
	} {
		if strings.Contains(raw, `"`+field+`"`) {
			t.Errorf("nil pointer field %q should be omitted from JSON, got: %s", field, raw)
		}
	}
}

func TestUpdateParams_ZeroValuePointersIncluded(t *testing.T) {
	emptyStr := ""
	zeroPri := 0
	zeroMins := 0

	params := UpdateParams{
		Title:            &emptyStr,
		Priority:         &zeroPri,
		Assignee:         &emptyStr,
		EstimatedMinutes: &zeroMins,
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	raw := string(data)
	// These should be present even though they hold zero/empty values,
	// because the pointer itself is non-nil.
	for _, field := range []string{"title", "priority", "assignee", "estimated_minutes"} {
		if !strings.Contains(raw, `"`+field+`"`) {
			t.Errorf("zero-value pointer field %q should be present in JSON, got: %s", field, raw)
		}
	}
}

func TestUpdateParams_JSONRoundTrip(t *testing.T) {
	title := "New title"
	status := "in_progress"
	pri := 2

	original := UpdateParams{
		Title:        &title,
		Status:       &status,
		Priority:     &pri,
		AddLabels:    []string{"urgent"},
		RemoveLabels: []string{"backlog"},
		Claim:        true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded UpdateParams
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Title == nil || *decoded.Title != title {
		t.Errorf("Title = %v, want %q", decoded.Title, title)
	}
	if decoded.Status == nil || *decoded.Status != status {
		t.Errorf("Status = %v, want %q", decoded.Status, status)
	}
	if decoded.Priority == nil || *decoded.Priority != pri {
		t.Errorf("Priority = %v, want %d", decoded.Priority, pri)
	}
	if len(decoded.AddLabels) != 1 || decoded.AddLabels[0] != "urgent" {
		t.Errorf("AddLabels = %v, want [urgent]", decoded.AddLabels)
	}
	if !decoded.Claim {
		t.Error("Claim = false, want true")
	}
	// Fields not set should remain nil.
	if decoded.Description != nil {
		t.Errorf("Description = %v, want nil", decoded.Description)
	}
}

// ---------------------------------------------------------------------------
// Section 7: BatchOp and BatchResult — json.RawMessage round-trip
// ---------------------------------------------------------------------------

func TestBatchOp_RawMessageRoundTrip(t *testing.T) {
	argsJSON := json.RawMessage(`{"title":"New issue","priority":1}`)

	op := BatchOp{
		Operation: "create",
		Args:      argsJSON,
	}

	data, err := json.Marshal(op)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded BatchOp
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Operation != "create" {
		t.Errorf("Operation = %q, want %q", decoded.Operation, "create")
	}

	// Verify the raw JSON is preserved.
	var argsMap map[string]any
	if err := json.Unmarshal(decoded.Args, &argsMap); err != nil {
		t.Fatalf("Unmarshal decoded Args: %v", err)
	}
	if argsMap["title"] != "New issue" {
		t.Errorf("Args.title = %v, want %q", argsMap["title"], "New issue")
	}
	if argsMap["priority"] != float64(1) {
		t.Errorf("Args.priority = %v, want 1", argsMap["priority"])
	}
}

func TestBatchResult_RawMessageRoundTrip(t *testing.T) {
	resultData := json.RawMessage(`{"id":"created-1","status":"open"}`)

	result := BatchResult{
		Success: true,
		Data:    resultData,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded BatchResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !decoded.Success {
		t.Error("Success = false, want true")
	}
	if decoded.Error != "" {
		t.Errorf("Error = %q, want empty", decoded.Error)
	}

	var dataMap map[string]any
	if err := json.Unmarshal(decoded.Data, &dataMap); err != nil {
		t.Fatalf("Unmarshal decoded Data: %v", err)
	}
	if dataMap["id"] != "created-1" {
		t.Errorf("Data.id = %v, want %q", dataMap["id"], "created-1")
	}
}

func TestBatchResult_ErrorCase(t *testing.T) {
	result := BatchResult{
		Success: false,
		Error:   "issue not found",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded BatchResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Success {
		t.Error("Success = true, want false")
	}
	if decoded.Error != "issue not found" {
		t.Errorf("Error = %q, want %q", decoded.Error, "issue not found")
	}
	if decoded.Data != nil {
		t.Errorf("Data = %s, want nil", decoded.Data)
	}
}

// ---------------------------------------------------------------------------
// Section 8: Mutation constants
// ---------------------------------------------------------------------------

func TestMutationConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		want     string
	}{
		{"MutationCreate", MutationCreate, "create"},
		{"MutationUpdate", MutationUpdate, "update"},
		{"MutationDelete", MutationDelete, "delete"},
		{"MutationComment", MutationComment, "comment"},
		{"MutationBonded", MutationBonded, "bonded"},
		{"MutationSquashed", MutationSquashed, "squashed"},
		{"MutationBurned", MutationBurned, "burned"},
		{"MutationStatus", MutationStatus, "status"},
		{"MutationRefresh", MutationRefresh, "refresh"},
		{"MutationSessionChange", MutationSessionChange, "session_change"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.constant, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Section 9: OmitEmpty correctness for optional fields
// ---------------------------------------------------------------------------

func TestIssueData_OmitEmptyFields(t *testing.T) {
	// Only required fields set; optional fields should be omitted.
	issue := IssueData{
		ID:        "minimal",
		Title:     "Minimal issue",
		Status:    "open",
		Priority:  0,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	data, err := json.Marshal(issue)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	raw := string(data)
	omittedFields := []string{
		"issue_type", "assignee", "owner", "labels", "source_repo",
		"parent", "due_at", "defer_until", "dependency_count", "dependent_count",
	}
	for _, field := range omittedFields {
		if strings.Contains(raw, `"`+field+`"`) {
			t.Errorf("empty %q should be omitted, got: %s", field, raw)
		}
	}

	// Fields without omitempty must be present.
	requiredFields := []string{"id", "title", "status", "priority", "created_at", "updated_at"}
	for _, field := range requiredFields {
		if !strings.Contains(raw, `"`+field+`"`) {
			t.Errorf("required field %q should be present, got: %s", field, raw)
		}
	}
}

// ---------------------------------------------------------------------------
// Section 10: CloseResult JSON round-trip
// ---------------------------------------------------------------------------

func TestCloseResult_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 1, 14, 0, 0, 0, time.UTC)
	closed := &IssueData{
		ID:        "closed-1",
		Title:     "Done task",
		Status:    "closed",
		Priority:  1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	unblocked := []IssueData{
		{
			ID:        "unblocked-1",
			Title:     "Freed task",
			Status:    "open",
			Priority:  2,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	original := CloseResult{Closed: closed, Unblocked: unblocked}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded CloseResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Closed == nil {
		t.Fatal("Closed is nil after round-trip")
	}
	if decoded.Closed.ID != "closed-1" {
		t.Errorf("Closed.ID = %q, want %q", decoded.Closed.ID, "closed-1")
	}
	if len(decoded.Unblocked) != 1 {
		t.Fatalf("Unblocked len = %d, want 1", len(decoded.Unblocked))
	}
	if decoded.Unblocked[0].ID != "unblocked-1" {
		t.Errorf("Unblocked[0].ID = %q, want %q", decoded.Unblocked[0].ID, "unblocked-1")
	}
}

// ---------------------------------------------------------------------------
// Section 11: ListOpts pointer fields — Priority filter
// ---------------------------------------------------------------------------

func TestListOpts_PriorityFilterNilVsZero(t *testing.T) {
	// nil Priority — should be omitted.
	opts := ListOpts{Status: "open"}
	data, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), `"priority"`) {
		t.Errorf("nil Priority should be omitted, got: %s", data)
	}

	// Zero Priority — should be present.
	zero := 0
	opts2 := ListOpts{Status: "open", Priority: &zero}
	data2, err := json.Marshal(opts2)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data2), `"priority":0`) {
		t.Errorf("zero-value *Priority should be present, got: %s", data2)
	}
}

// ---------------------------------------------------------------------------
// Section 12: CommentData with optional pointer fields
// ---------------------------------------------------------------------------

func TestCommentData_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	editedAt := now.Add(30 * time.Minute)
	parentID := int64(5)

	original := CommentData{
		ID:        42,
		IssueID:   "issue-1",
		Author:    "judy",
		Text:      "Looks good to me",
		CreatedAt: now,
		ParentID:  &parentID,
		EditedAt:  &editedAt,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded CommentData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID = %d, want %d", decoded.ID, original.ID)
	}
	if decoded.ParentID == nil || *decoded.ParentID != parentID {
		t.Errorf("ParentID = %v, want %d", decoded.ParentID, parentID)
	}
	if decoded.EditedAt == nil || !decoded.EditedAt.Equal(editedAt) {
		t.Errorf("EditedAt = %v, want %v", decoded.EditedAt, editedAt)
	}
}

func TestCommentData_NilOptionalsOmitted(t *testing.T) {
	comment := CommentData{
		ID:        1,
		IssueID:   "issue-1",
		Author:    "karl",
		Text:      "Hello",
		CreatedAt: time.Now().UTC(),
		// ParentID and EditedAt left nil.
	}

	data, err := json.Marshal(comment)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	raw := string(data)
	if strings.Contains(raw, "parent_id") {
		t.Errorf("nil ParentID should be omitted, got: %s", raw)
	}
	if strings.Contains(raw, "edited_at") {
		t.Errorf("nil EditedAt should be omitted, got: %s", raw)
	}
}
