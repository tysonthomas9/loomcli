package fleet

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/entity"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// testTime returns a stable UTC time for test determinism.
func testTime() time.Time {
	return time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
}

// fullIssue returns a types.Issue with every field populated.
func fullIssue() *types.Issue {
	now := testTime()
	extRef := "gh-42"
	est := 120
	qs := float32(0.85)
	return &types.Issue{
		ID:                 "test-123",
		Title:              "Full Issue",
		Description:        "description text",
		Design:             "design text",
		AcceptanceCriteria: "ac text",
		Notes:              "notes text",
		Status:             types.StatusInProgress,
		Priority:           1,
		IssueType:          types.TypeFeature,
		Assignee:           "agent-1",
		Owner:              "owner@test.com",
		EstimatedMinutes:   &est,
		CreatedAt:          now,
		CreatedBy:          "creator",
		UpdatedAt:          now.Add(time.Hour),
		ClosedAt:           nil,
		CloseReason:        "",
		ClosedBySession:    "",
		DueAt:              &now,
		DeferUntil:         &now,
		ExternalRef:        &extRef,
		SourceSystem:       "github",
		SourceRepo:         "repo-1",
		Labels:             []string{"label-1", "label-2"},
		Dependencies: []*types.Dependency{
			{IssueID: "test-123", DependsOnID: "dep-1", Type: types.DepBlocks, CreatedAt: now, CreatedBy: "user", Metadata: `{"key":"val"}`, ThreadID: "thread-1"},
		},
		Comments: []*types.Comment{
			{ID: 1, IssueID: "test-123", Author: "user", Text: "comment", CreatedAt: now},
		},
		DeletedAt:      nil,
		DeletedBy:      "",
		DeleteReason:   "",
		OriginalType:   "",
		Sender:         "agent-1",
		Ephemeral:      true,
		Pinned:         true,
		IsTemplate:     true,
		BondedFrom:     []types.BondRef{{SourceID: "src-1", BondType: "sequential", BondPoint: "bp-1"}},
		Creator:        &types.EntityRef{Name: "polecat/Nux", Platform: "gastown", Org: "steveyegge", ID: "polecat-nux"},
		Validations:    []types.Validation{{Validator: &types.EntityRef{Name: "v1"}, Outcome: "accepted", Timestamp: now, Score: &qs}},
		QualityScore:   &qs,
		Crystallizes:   true,
		AwaitType:      "gh:pr",
		AwaitID:        "pr-99",
		Timeout:        5 * time.Minute,
		Waiters:        []string{"user@test.com"},
		Holder:         "holder-1",
		SourceFormula:  "formula-1",
		SourceLocation: "steps[0]",
		AgentState:     types.StateRunning,
		LastActivity:   &now,
		RoleType:       "polecat",
		Rig:            "rig-alpha",
		MolType:        types.MolTypeSwarm,
		WorkType:       types.WorkTypeOpenCompetition,
		EventKind:      "patrol.started",
		Actor:          "entity://hop/gastown/org/actor-1",
		Target:         "bead-42",
		Payload:        `{"event":"data"}`,
	}
}

func TestIssueToEntity_FullFidelity(t *testing.T) {
	issue := fullIssue()
	e := IssueToEntity(issue)

	// Core Identification
	assertEqual(t, "ID", e.ID, "test-123")

	// Issue Content
	assertEqual(t, "Title", e.Title, "Full Issue")
	assertEqual(t, "Description", e.Description, "description text")
	assertEqual(t, "Design", e.Design, "design text")
	assertEqual(t, "AcceptanceCriteria", e.AcceptanceCriteria, "ac text")
	assertEqual(t, "Notes", e.Notes, "notes text")

	// Status & Workflow
	assertEqual(t, "Status", string(e.Status), "in_progress")
	assertEqualInt(t, "Priority", e.Priority, 1)
	assertEqual(t, "IssueType", string(e.IssueType), "feature")

	// Assignment
	assertEqual(t, "Assignee", e.Assignee, "agent-1")
	assertEqual(t, "Owner", e.Owner, "owner@test.com")
	if e.EstimatedMinutes == nil || *e.EstimatedMinutes != 120 {
		t.Errorf("EstimatedMinutes = %v, want 120", e.EstimatedMinutes)
	}

	// Timestamps
	assertEqualTime(t, "CreatedAt", e.CreatedAt, issue.CreatedAt)
	assertEqual(t, "CreatedBy", e.CreatedBy, "creator")
	assertEqualTime(t, "UpdatedAt", e.UpdatedAt, issue.UpdatedAt)

	// Time-Based Scheduling
	if e.DueAt == nil || !e.DueAt.Equal(*issue.DueAt) {
		t.Errorf("DueAt mismatch")
	}
	if e.DeferUntil == nil || !e.DeferUntil.Equal(*issue.DeferUntil) {
		t.Errorf("DeferUntil mismatch")
	}

	// External Integration
	if e.ExternalRef == nil || *e.ExternalRef != "gh-42" {
		t.Errorf("ExternalRef = %v, want gh-42", e.ExternalRef)
	}
	assertEqual(t, "SourceSystem", e.SourceSystem, "github")
	assertEqual(t, "SourceRepo", e.SourceRepo, "repo-1")

	// Relational Data
	if len(e.Labels) != 2 {
		t.Errorf("Labels len = %d, want 2", len(e.Labels))
	}
	if len(e.Dependencies) != 1 {
		t.Fatalf("Dependencies len = %d, want 1", len(e.Dependencies))
	}
	if e.Dependencies[0].Metadata != `{"key":"val"}` {
		t.Errorf("Dependency Metadata = %q, want JSON", e.Dependencies[0].Metadata)
	}
	if e.Dependencies[0].ThreadID != "thread-1" {
		t.Errorf("Dependency ThreadID = %q, want %q", e.Dependencies[0].ThreadID, "thread-1")
	}
	if len(e.Comments) != 1 {
		t.Fatalf("Comments len = %d, want 1", len(e.Comments))
	}

	// Messaging Fields
	assertEqual(t, "Sender", e.Sender, "agent-1")
	assertEqualBool(t, "Ephemeral", e.Ephemeral, true)

	// Context Markers
	assertEqualBool(t, "Pinned", e.Pinned, true)
	assertEqualBool(t, "IsTemplate", e.IsTemplate, true)

	// Bonding Fields
	if len(e.BondedFrom) != 1 {
		t.Fatalf("BondedFrom len = %d, want 1", len(e.BondedFrom))
	}
	assertEqual(t, "BondedFrom[0].SourceID", e.BondedFrom[0].SourceID, "src-1")
	assertEqual(t, "BondedFrom[0].BondType", e.BondedFrom[0].BondType, "sequential")
	assertEqual(t, "BondedFrom[0].BondPoint", e.BondedFrom[0].BondPoint, "bp-1")

	// HOP Fields
	if e.Creator == nil {
		t.Fatal("Creator should not be nil")
	}
	assertEqual(t, "Creator.Name", e.Creator.Name, "polecat/Nux")
	assertEqual(t, "Creator.Platform", e.Creator.Platform, "gastown")
	assertEqual(t, "Creator.Org", e.Creator.Org, "steveyegge")
	assertEqual(t, "Creator.ID", e.Creator.ID, "polecat-nux")

	if len(e.Validations) != 1 {
		t.Fatalf("Validations len = %d, want 1", len(e.Validations))
	}
	assertEqual(t, "Validations[0].Outcome", e.Validations[0].Outcome, "accepted")
	if e.Validations[0].Validator == nil {
		t.Fatal("Validations[0].Validator should not be nil")
	}
	if e.Validations[0].Score == nil || *e.Validations[0].Score != 0.85 {
		t.Errorf("Validations[0].Score = %v, want 0.85", e.Validations[0].Score)
	}

	if e.QualityScore == nil || *e.QualityScore != 0.85 {
		t.Errorf("QualityScore = %v, want 0.85", e.QualityScore)
	}
	assertEqualBool(t, "Crystallizes", e.Crystallizes, true)

	// Gate Fields
	assertEqual(t, "AwaitType", e.AwaitType, "gh:pr")
	assertEqual(t, "AwaitID", e.AwaitID, "pr-99")
	if e.Timeout != 5*time.Minute {
		t.Errorf("Timeout = %v, want 5m", e.Timeout)
	}
	if len(e.Waiters) != 1 || e.Waiters[0] != "user@test.com" {
		t.Errorf("Waiters = %v, want [user@test.com]", e.Waiters)
	}

	// Slot Fields
	assertEqual(t, "Holder", e.Holder, "holder-1")

	// Source Tracing Fields
	assertEqual(t, "SourceFormula", e.SourceFormula, "formula-1")
	assertEqual(t, "SourceLocation", e.SourceLocation, "steps[0]")

	// Agent Identity Fields
	assertEqual(t, "AgentState", string(e.AgentState), "running")
	if e.LastActivity == nil || !e.LastActivity.Equal(testTime()) {
		t.Errorf("LastActivity mismatch")
	}
	assertEqual(t, "RoleType", e.RoleType, "polecat")
	assertEqual(t, "Rig", e.Rig, "rig-alpha")

	// Molecule Type Fields
	assertEqual(t, "MolType", string(e.MolType), "swarm")

	// Work Type Fields
	assertEqual(t, "WorkType", string(e.WorkType), "open_competition")

	// Event Fields
	assertEqual(t, "EventKind", e.EventKind, "patrol.started")
	assertEqual(t, "Actor", e.Actor, "entity://hop/gastown/org/actor-1")
	assertEqual(t, "Target", e.Target, "bead-42")
	assertEqual(t, "Payload", e.Payload, `{"event":"data"}`)
}

func TestIssueToEntity_NilInput(t *testing.T) {
	e := IssueToEntity(nil)
	if e.ID != "" {
		t.Errorf("ID = %q, want empty", e.ID)
	}
	if e.Labels == nil {
		t.Error("Labels should be empty slice, not nil")
	}
	if len(e.Labels) != 0 {
		t.Errorf("Labels len = %d, want 0", len(e.Labels))
	}
}

func TestIssueToEntity_ZeroValue(t *testing.T) {
	issue := &types.Issue{}
	e := IssueToEntity(issue)

	if e.Labels == nil {
		t.Error("Labels should be empty slice, not nil")
	}
	if e.Dependencies == nil {
		t.Error("Dependencies should be empty slice, not nil")
	}
	if e.Comments == nil {
		t.Error("Comments should be empty slice, not nil")
	}
}

func TestIssueToEntity_NilLabels(t *testing.T) {
	issue := &types.Issue{
		ID:    "test-1",
		Title: "X",
	}
	e := IssueToEntity(issue)
	if e.Labels == nil {
		t.Error("Labels should be empty slice, not nil")
	}
	if len(e.Labels) != 0 {
		t.Errorf("Labels len = %d, want 0", len(e.Labels))
	}
}

func TestIssueToEntity_NilExternalRef(t *testing.T) {
	issue := &types.Issue{ID: "test-1"}
	e := IssueToEntity(issue)
	if e.ExternalRef != nil {
		t.Errorf("ExternalRef = %v, want nil", e.ExternalRef)
	}
}

func TestIssueToEntity_NilCreator(t *testing.T) {
	issue := &types.Issue{ID: "test-1"}
	e := IssueToEntity(issue)
	if e.Creator != nil {
		t.Errorf("Creator = %v, want nil", e.Creator)
	}
}

func TestIssueToEntity_TombstoneFields(t *testing.T) {
	now := testTime()
	issue := &types.Issue{
		ID:           "tomb-1",
		Status:       types.StatusTombstone,
		DeletedAt:    &now,
		DeletedBy:    "admin",
		DeleteReason: "obsolete",
		OriginalType: "task",
	}
	e := IssueToEntity(issue)
	if e.DeletedAt == nil || !e.DeletedAt.Equal(now) {
		t.Error("DeletedAt mismatch")
	}
	assertEqual(t, "DeletedBy", e.DeletedBy, "admin")
	assertEqual(t, "DeleteReason", e.DeleteReason, "obsolete")
	assertEqual(t, "OriginalType", e.OriginalType, "task")
}

func TestIssueToEntity_EnumCasting(t *testing.T) {
	issue := &types.Issue{
		ID:         "test-1",
		Status:     types.Status("custom_status"),
		IssueType:  types.IssueType("molecule"),
		AgentState: types.AgentState("stuck"),
		MolType:    types.MolType("patrol"),
		WorkType:   types.WorkType("open_competition"),
	}
	e := IssueToEntity(issue)
	assertEqual(t, "Status", string(e.Status), "custom_status")
	assertEqual(t, "IssueType", string(e.IssueType), "molecule")
	assertEqual(t, "AgentState", string(e.AgentState), "stuck")
	assertEqual(t, "MolType", string(e.MolType), "patrol")
	assertEqual(t, "WorkType", string(e.WorkType), "open_competition")
}

func TestIssuesToEntities(t *testing.T) {
	now := testTime()
	issues := []*types.Issue{
		{ID: "a", Title: "A", CreatedAt: now, UpdatedAt: now},
		nil, // should be filtered
		{ID: "b", Title: "B", CreatedAt: now, UpdatedAt: now},
	}
	result := IssuesToEntities(issues)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	assertEqual(t, "result[0].ID", result[0].ID, "a")
	assertEqual(t, "result[1].ID", result[1].ID, "b")
}

func TestIssuesToEntities_Nil(t *testing.T) {
	result := IssuesToEntities(nil)
	if result == nil {
		t.Error("result should be empty slice, not nil")
	}
	if len(result) != 0 {
		t.Errorf("len = %d, want 0", len(result))
	}
}

func TestIssuesToEntities_Empty(t *testing.T) {
	result := IssuesToEntities([]*types.Issue{})
	if result == nil {
		t.Error("result should be empty slice, not nil")
	}
}

func TestDetailsToEntity(t *testing.T) {
	now := testTime()
	details := &types.IssueDetails{
		Issue: types.Issue{
			ID:        "parent-1",
			Title:     "Parent",
			Status:    types.StatusInProgress,
			CreatedAt: now,
			UpdatedAt: now,
		},
		Labels: []string{"label-a", "label-b"},
		Dependencies: []*types.IssueWithDependencyMetadata{
			{Issue: types.Issue{ID: "dep-1", Title: "Dep1", CreatedAt: now, CreatedBy: "user1"}, DependencyType: types.DepBlocks},
			{Issue: types.Issue{ID: "dep-2", Title: "Dep2", CreatedAt: now, CreatedBy: "user2"}, DependencyType: types.DepBlocks},
			nil, // should be filtered
			{Issue: types.Issue{ID: "dep-3", Title: "Dep3", CreatedAt: now, CreatedBy: "user3"}, DependencyType: types.DepRelated},
		},
		Dependents: []*types.IssueWithDependencyMetadata{
			{Issue: types.Issue{ID: "child-1", Title: "Child1", CreatedAt: now, CreatedBy: "user4"}, DependencyType: types.DepBlocks},
			{Issue: types.Issue{ID: "child-2", Title: "Child2", CreatedAt: now, CreatedBy: "user5"}, DependencyType: types.DepParentChild},
		},
		Comments: []*types.Comment{
			{ID: 1, IssueID: "parent-1", Author: "a", Text: "c1", CreatedAt: now},
			{ID: 2, IssueID: "parent-1", Author: "b", Text: "c2", CreatedAt: now},
			{ID: 3, IssueID: "parent-1", Author: "c", Text: "c3", CreatedAt: now},
			{ID: 4, IssueID: "parent-1", Author: "d", Text: "c4", CreatedAt: now},
		},
		Parent: strPtr("epic-1"),
	}

	e := DetailsToEntity(details)

	assertEqual(t, "ID", e.ID, "parent-1")

	// Labels from details (not from issue).
	if len(e.Labels) != 2 {
		t.Errorf("Labels len = %d, want 2", len(e.Labels))
	}

	// Dependencies: 3 deps + 2 dependents = 5 combined.
	if len(e.Dependencies) != 5 {
		t.Fatalf("Dependencies len = %d, want 5", len(e.Dependencies))
	}

	// Comments.
	if len(e.Comments) != 4 {
		t.Fatalf("Comments len = %d, want 4", len(e.Comments))
	}
}

func TestDetailsToEntity_DependencyDirection(t *testing.T) {
	now := testTime()
	details := &types.IssueDetails{
		Issue: types.Issue{ID: "parent-1", Title: "P", CreatedAt: now, UpdatedAt: now},
		Dependencies: []*types.IssueWithDependencyMetadata{
			{Issue: types.Issue{ID: "dep-x", CreatedAt: now}, DependencyType: types.DepBlocks},
		},
		Dependents: []*types.IssueWithDependencyMetadata{
			{Issue: types.Issue{ID: "child-y", CreatedAt: now}, DependencyType: types.DepBlocks},
		},
	}

	e := DetailsToEntity(details)

	if len(e.Dependencies) != 2 {
		t.Fatalf("Dependencies len = %d, want 2", len(e.Dependencies))
	}

	// Dependency (parent-1 depends on dep-x): IssueID=parent-1, DependsOnID=dep-x
	dep := e.Dependencies[0]
	assertEqual(t, "dep.IssueID", dep.IssueID, "parent-1")
	assertEqual(t, "dep.DependsOnID", dep.DependsOnID, "dep-x")

	// Dependent (child-y depends on parent-1): IssueID=child-y, DependsOnID=parent-1
	dependent := e.Dependencies[1]
	assertEqual(t, "dependent.IssueID", dependent.IssueID, "child-y")
	assertEqual(t, "dependent.DependsOnID", dependent.DependsOnID, "parent-1")
}

func TestDetailsToEntity_EmptyRelations(t *testing.T) {
	now := testTime()
	details := &types.IssueDetails{
		Issue: types.Issue{ID: "test-1", Title: "T", CreatedAt: now, UpdatedAt: now},
	}
	e := DetailsToEntity(details)

	if e.Labels == nil {
		t.Error("Labels should be empty slice, not nil")
	}
	if e.Dependencies == nil {
		t.Error("Dependencies should be empty slice, not nil")
	}
	if e.Comments == nil {
		t.Error("Comments should be empty slice, not nil")
	}
}

func TestDetailsToEntity_NilInput(t *testing.T) {
	e := DetailsToEntity(nil)
	if e.ID != "" {
		t.Errorf("ID = %q, want empty", e.ID)
	}
	if e.Labels == nil {
		t.Error("Labels should be empty slice, not nil")
	}
}

func TestDependencyToEntity(t *testing.T) {
	now := testTime()
	dep := &types.Dependency{
		IssueID:     "issue-1",
		DependsOnID: "dep-1",
		Type:        types.DepBlocks,
		CreatedAt:   now,
		CreatedBy:   "user",
		Metadata:    `{"key":"val"}`,
		ThreadID:    "thread-1",
	}
	e := DependencyToEntity(dep)
	if e == nil {
		t.Fatal("result should not be nil")
	}
	assertEqual(t, "IssueID", e.IssueID, "issue-1")
	assertEqual(t, "DependsOnID", e.DependsOnID, "dep-1")
	assertEqual(t, "Type", string(e.Type), "blocks")
	assertEqualTime(t, "CreatedAt", e.CreatedAt, now)
	assertEqual(t, "CreatedBy", e.CreatedBy, "user")
	assertEqual(t, "Metadata", e.Metadata, `{"key":"val"}`)
	assertEqual(t, "ThreadID", e.ThreadID, "thread-1")
}

func TestDependencyToEntity_Nil(t *testing.T) {
	e := DependencyToEntity(nil)
	if e != nil {
		t.Errorf("result = %v, want nil", e)
	}
}

func TestDependenciesToEntities(t *testing.T) {
	now := testTime()
	deps := []*types.Dependency{
		{IssueID: "a", DependsOnID: "b", Type: types.DepBlocks, CreatedAt: now},
		nil,
		{IssueID: "c", DependsOnID: "d", Type: types.DepRelated, CreatedAt: now},
	}
	result := DependenciesToEntities(deps)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
}

func TestDependenciesToEntities_Nil(t *testing.T) {
	result := DependenciesToEntities(nil)
	if result == nil {
		t.Error("result should be empty slice, not nil")
	}
}

func TestCommentToEntity(t *testing.T) {
	now := testTime()
	c := &types.Comment{
		ID:        42,
		IssueID:   "test-1",
		Author:    "user",
		Text:      "hello",
		CreatedAt: now,
	}
	e := CommentToEntity(c)
	if e == nil {
		t.Fatal("result should not be nil")
	}
	if e.ID != 42 {
		t.Errorf("ID = %d, want 42", e.ID)
	}
	assertEqual(t, "IssueID", e.IssueID, "test-1")
	assertEqual(t, "Author", e.Author, "user")
	assertEqual(t, "Text", e.Text, "hello")
	assertEqualTime(t, "CreatedAt", e.CreatedAt, now)
}

func TestCommentToEntity_Nil(t *testing.T) {
	e := CommentToEntity(nil)
	if e != nil {
		t.Errorf("result = %v, want nil", e)
	}
}

func TestCommentToEntity_ZeroID(t *testing.T) {
	c := &types.Comment{ID: 0, IssueID: "test-1", Author: "u", Text: "t", CreatedAt: testTime()}
	e := CommentToEntity(c)
	if e == nil {
		t.Fatal("result should not be nil")
	}
	if e.ID != 0 {
		t.Errorf("ID = %d, want 0", e.ID)
	}
}

func TestCommentsToEntities_Nil(t *testing.T) {
	result := CommentsToEntities(nil)
	if result == nil {
		t.Error("result should be empty slice, not nil")
	}
}

func TestEntityRefToEntity(t *testing.T) {
	ref := &types.EntityRef{
		Name:     "polecat/Nux",
		Platform: "gastown",
		Org:      "steveyegge",
		ID:       "polecat-nux",
	}
	e := entityRefToEntity(ref)
	if e == nil {
		t.Fatal("result should not be nil")
	}
	assertEqual(t, "Name", e.Name, "polecat/Nux")
	assertEqual(t, "Platform", e.Platform, "gastown")
	assertEqual(t, "Org", e.Org, "steveyegge")
	assertEqual(t, "ID", e.ID, "polecat-nux")
}

func TestEntityRefToEntity_Nil(t *testing.T) {
	e := entityRefToEntity(nil)
	if e != nil {
		t.Errorf("result = %v, want nil", e)
	}
}

func TestEntityRefToEntity_Empty(t *testing.T) {
	ref := &types.EntityRef{}
	e := entityRefToEntity(ref)
	if e == nil {
		t.Fatal("result should not be nil")
	}
	if !e.IsEmpty() {
		t.Error("empty ref should produce empty entity ref")
	}
}

func TestValidationsToEntities(t *testing.T) {
	now := testTime()
	score := float32(0.9)
	vs := []types.Validation{
		{
			Validator: &types.EntityRef{Name: "v1", Platform: "p1"},
			Outcome:   "accepted",
			Timestamp: now,
			Score:     &score,
		},
		{
			Validator: nil,
			Outcome:   "rejected",
			Timestamp: now,
		},
	}
	result := validationsToEntities(vs)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].Validator == nil {
		t.Error("result[0].Validator should not be nil")
	}
	assertEqual(t, "result[0].Outcome", result[0].Outcome, "accepted")
	if result[0].Score == nil || *result[0].Score != 0.9 {
		t.Errorf("result[0].Score = %v, want 0.9", result[0].Score)
	}
	if result[1].Validator != nil {
		t.Error("result[1].Validator should be nil")
	}
}

func TestValidationsToEntities_Nil(t *testing.T) {
	result := validationsToEntities(nil)
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
}

func TestBondRefsToEntities(t *testing.T) {
	refs := []types.BondRef{
		{SourceID: "src-1", BondType: "sequential", BondPoint: "bp-1"},
		{SourceID: "src-2", BondType: "parallel"},
	}
	result := bondRefsToEntities(refs)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	assertEqual(t, "result[0].SourceID", result[0].SourceID, "src-1")
	assertEqual(t, "result[0].BondType", result[0].BondType, "sequential")
	assertEqual(t, "result[0].BondPoint", result[0].BondPoint, "bp-1")
	assertEqual(t, "result[1].BondPoint", result[1].BondPoint, "")
}

func TestBondRefsToEntities_Nil(t *testing.T) {
	result := bondRefsToEntities(nil)
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
}

// --- ClaimResultToEntity tests ---

func TestClaimResultToEntity_HappyPath(t *testing.T) {
	now := testTime()
	cr := &ClaimResult{
		Payload: &types.WorkHandoffPayload{
			Issue: &types.Issue{
				ID:       "task-1",
				Title:    "Claimed",
				Status:   types.StatusInProgress,
				Priority: 3,
			},
			Labels: []string{"fleet", "urgent"},
			Dependencies: []*types.Dependency{
				{IssueID: "task-1", DependsOnID: "dep-1", Type: types.DepBlocks, CreatedAt: now},
				{IssueID: "task-1", DependsOnID: "dep-2", Type: types.DepRelated, CreatedAt: now},
			},
		},
	}

	e, err := ClaimResultToEntity(cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e == nil {
		t.Fatal("result should not be nil")
	}
	assertEqual(t, "ID", e.ID, "task-1")
	assertEqual(t, "Title", e.Title, "Claimed")
	if len(e.Labels) != 2 {
		t.Errorf("Labels len = %d, want 2", len(e.Labels))
	}
	if len(e.Dependencies) != 2 {
		t.Fatalf("Dependencies len = %d, want 2", len(e.Dependencies))
	}
}

func TestClaimResultToEntity_NilCR(t *testing.T) {
	_, err := ClaimResultToEntity(nil)
	if err == nil {
		t.Fatal("expected error for nil ClaimResult")
	}
}

func TestClaimResultToEntity_NilPayload(t *testing.T) {
	cr := &ClaimResult{Payload: nil}
	_, err := ClaimResultToEntity(cr)
	if err == nil {
		t.Fatal("expected error for nil Payload")
	}
}

func TestClaimResultToEntity_NilIssue(t *testing.T) {
	cr := &ClaimResult{Payload: &types.WorkHandoffPayload{Issue: nil}}
	_, err := ClaimResultToEntity(cr)
	if err == nil {
		t.Fatal("expected error for nil Issue")
	}
}

func TestClaimResultToEntity_PriorityOverride(t *testing.T) {
	override := 1
	cr := &ClaimResult{
		Payload: &types.WorkHandoffPayload{
			Issue: &types.Issue{
				ID:       "task-1",
				Title:    "Override",
				Priority: 3,
			},
			PriorityOverride: &override,
		},
	}

	e, err := ClaimResultToEntity(cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Priority != 1 {
		t.Errorf("Priority = %d, want 1 (overridden from 3)", e.Priority)
	}
}

func TestClaimResultToEntity_Deadline(t *testing.T) {
	deadline := testTime().Add(24 * time.Hour)
	cr := &ClaimResult{
		Payload: &types.WorkHandoffPayload{
			Issue: &types.Issue{
				ID:    "task-1",
				Title: "Deadline",
			},
			Deadline: &deadline,
		},
	}

	e, err := ClaimResultToEntity(cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.DeferUntil == nil || !e.DeferUntil.Equal(deadline) {
		t.Errorf("DeferUntil = %v, want %v", e.DeferUntil, deadline)
	}
}

func TestClaimResultToEntity_NilDependencies(t *testing.T) {
	cr := &ClaimResult{
		Payload: &types.WorkHandoffPayload{
			Issue: &types.Issue{ID: "task-1", Title: "NoDeps"},
		},
	}

	e, err := ClaimResultToEntity(cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Dependencies == nil {
		t.Error("Dependencies should be empty slice, not nil")
	}
	if len(e.Dependencies) != 0 {
		t.Errorf("Dependencies len = %d, want 0", len(e.Dependencies))
	}
}

func TestClaimResultToEntity_NilLabels(t *testing.T) {
	cr := &ClaimResult{
		Payload: &types.WorkHandoffPayload{
			Issue: &types.Issue{ID: "task-1", Title: "NoLabels"},
		},
	}

	e, err := ClaimResultToEntity(cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Labels == nil {
		t.Error("Labels should be empty slice, not nil")
	}
	if len(e.Labels) != 0 {
		t.Errorf("Labels len = %d, want 0", len(e.Labels))
	}
}

// --- Test helpers ---

func assertEqual(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func assertEqualInt(t *testing.T, field string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d, want %d", field, got, want)
	}
}

func assertEqualBool(t *testing.T, field string, got, want bool) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

func assertEqualTime(t *testing.T, field string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

func strPtr(s string) *string { return &s }

// Verify interface satisfaction at compile time for exported functions.
var _ = IssueToEntity
var _ = IssuesToEntities
var _ = DetailsToEntity
var _ = DependencyToEntity
var _ = DependenciesToEntities
var _ = CommentToEntity
var _ = CommentsToEntities
var _ = ClaimResultToEntity

// Verify entity types are correct at compile time.
var _ entity.Issue = IssueToEntity(nil)
