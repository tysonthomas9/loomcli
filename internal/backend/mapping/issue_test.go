package mapping

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/entity"
)

// ---------------------------------------------------------------------------
// IssueFromData
// ---------------------------------------------------------------------------

func TestIssueFromData(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	due := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	defer_ := time.Date(2026, 1, 20, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		input  backend.IssueData
		verify func(t *testing.T, e entity.Issue)
	}{
		{
			name: "fully populated IssueData maps all 13 fields",
			input: backend.IssueData{
				ID:              "issue-42",
				Title:           "Implement widget",
				Status:          "in_progress",
				Priority:        2,
				IssueType:       "feature",
				Assignee:        "alice",
				Owner:           "bob",
				Labels:          []string{"backend", "urgent"},
				SourceRepo:      "loomcli",
				Parent:          "epic-1",
				CreatedAt:       now,
				UpdatedAt:       now.Add(time.Hour),
				DueAt:           &due,
				DeferUntil:      &defer_,
				DependencyCount: 5,
				DependentCount:  3,
			},
			verify: func(t *testing.T, e entity.Issue) {
				// 13 shared fields.
				require.Equal(t, "issue-42", e.ID)
				require.Equal(t, "Implement widget", e.Title)
				require.Equal(t, entity.IssueStatus("in_progress"), e.Status)
				require.Equal(t, 2, e.Priority)
				require.Equal(t, entity.IssueType("feature"), e.IssueType)
				require.Equal(t, "alice", e.Assignee)
				require.Equal(t, "bob", e.Owner)
				require.Equal(t, []string{"backend", "urgent"}, e.Labels)
				require.Equal(t, "loomcli", e.SourceRepo)
				require.True(t, e.CreatedAt.Equal(now))
				require.True(t, e.UpdatedAt.Equal(now.Add(time.Hour)))
				require.NotNil(t, e.DueAt)
				require.True(t, e.DueAt.Equal(due))
				require.NotNil(t, e.DeferUntil)
				require.True(t, e.DeferUntil.Equal(defer_))

				// Fields not present in IssueData must be zero-valued.
				require.Empty(t, e.Description)
				require.Empty(t, e.Design)
				require.Empty(t, e.AcceptanceCriteria)
				require.Empty(t, e.Notes)
				require.Nil(t, e.Creator)
				require.Nil(t, e.Validations)
				require.Nil(t, e.QualityScore)
				require.False(t, e.Crystallizes)
				require.Empty(t, e.AwaitType)
				require.Empty(t, e.AwaitID)
				require.Zero(t, e.Timeout)
				require.Nil(t, e.Waiters)
				require.Nil(t, e.BondedFrom)
				require.Nil(t, e.DeletedAt)
				require.Empty(t, e.DeletedBy)
				require.Empty(t, e.DeleteReason)
				require.Empty(t, e.Sender)
				require.False(t, e.Ephemeral)
				require.False(t, e.Pinned)
				require.False(t, e.IsTemplate)
				require.Empty(t, e.Holder)
				require.Empty(t, e.SourceFormula)
				require.Empty(t, e.SourceLocation)
				require.Empty(t, e.HookBead)
				require.Empty(t, e.RoleBead)
				require.Empty(t, string(e.AgentState))
				require.Nil(t, e.LastActivity)
				require.Empty(t, e.RoleType)
				require.Empty(t, e.Rig)
				require.Empty(t, string(e.MolType))
				require.Empty(t, string(e.WorkType))
				require.Empty(t, e.EventKind)
				require.Empty(t, e.Actor)
				require.Empty(t, e.Target)
				require.Empty(t, e.Payload)
				require.Empty(t, e.SourceSystem)
				require.Nil(t, e.ExternalRef)
			},
		},
		{
			name:  "minimal IssueData (zero values) produces zero-valued entity with non-nil empty Labels",
			input: backend.IssueData{},
			verify: func(t *testing.T, e entity.Issue) {
				require.Empty(t, e.ID)
				require.Empty(t, e.Title)
				require.Equal(t, entity.IssueStatus(""), e.Status)
				require.Zero(t, e.Priority)
				require.Equal(t, entity.IssueType(""), e.IssueType)
				require.Empty(t, e.Assignee)
				require.Empty(t, e.Owner)
				require.NotNil(t, e.Labels)
				require.Empty(t, e.Labels)
				require.Empty(t, e.SourceRepo)
				require.True(t, e.CreatedAt.IsZero())
				require.True(t, e.UpdatedAt.IsZero())
				require.Nil(t, e.DueAt)
				require.Nil(t, e.DeferUntil)
			},
		},
		{
			name: "DueAt and DeferUntil set as time.Time pointers",
			input: backend.IssueData{
				DueAt:      &due,
				DeferUntil: &defer_,
			},
			verify: func(t *testing.T, e entity.Issue) {
				require.NotNil(t, e.DueAt)
				require.True(t, e.DueAt.Equal(due))
				require.NotNil(t, e.DeferUntil)
				require.True(t, e.DeferUntil.Equal(defer_))
			},
		},
		{
			name: "nil Labels in IssueData produces non-nil empty slice in entity",
			input: backend.IssueData{
				Labels: nil,
			},
			verify: func(t *testing.T, e entity.Issue) {
				require.NotNil(t, e.Labels)
				require.Empty(t, e.Labels)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IssueFromData(tt.input)
			tt.verify(t, got)
		})
	}
}

// ---------------------------------------------------------------------------
// IssueFromDetailData
// ---------------------------------------------------------------------------

func TestIssueFromDetailData(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	closedAt := now.Add(24 * time.Hour)
	estMins := 60

	tests := []struct {
		name   string
		input  backend.IssueDetailData
		verify func(t *testing.T, e entity.Issue)
	}{
		{
			name: "fully populated IssueDetailData including content, deps, comments",
			input: backend.IssueDetailData{
				IssueData: backend.IssueData{
					ID:        "detail-1",
					Title:     "Full detail issue",
					Status:    "open",
					Priority:  1,
					IssueType: "task",
					Assignee:  "carol",
					Owner:     "dan",
					Labels:    []string{"api"},
					CreatedAt: now,
					UpdatedAt: now.Add(time.Hour),
				},
				Description:        "A detailed description",
				Design:             "Design doc",
				AcceptanceCriteria: "Must pass all tests",
				Notes:              "Some notes",
				CreatedBy:          "eve",
				ClosedAt:           &closedAt,
				CloseReason:        "completed",
				ClosedBySession:    "ses-abc",
				ExternalRef:        "gh-42",
				EstimatedMinutes:   &estMins,
				Dependencies: []backend.DependencyData{
					{IssueID: "detail-1", DependsOnID: "blocker-1", Type: "blocks", CreatedAt: now, CreatedBy: "eve"},
				},
				Dependents: []backend.DependencyData{
					{IssueID: "child-1", DependsOnID: "detail-1", Type: "blocks", CreatedAt: now},
				},
				Comments: []backend.CommentData{
					{ID: 1, IssueID: "detail-1", Author: "frank", Text: "LGTM", CreatedAt: now},
				},
			},
			verify: func(t *testing.T, e entity.Issue) {
				// Core fields from IssueData.
				require.Equal(t, "detail-1", e.ID)
				require.Equal(t, "Full detail issue", e.Title)
				require.Equal(t, entity.StatusOpen, e.Status)
				require.Equal(t, 1, e.Priority)
				require.Equal(t, entity.TypeTask, e.IssueType)
				require.Equal(t, "carol", e.Assignee)
				require.Equal(t, "dan", e.Owner)
				require.Equal(t, []string{"api"}, e.Labels)

				// Content fields.
				require.Equal(t, "A detailed description", e.Description)
				require.Equal(t, "Design doc", e.Design)
				require.Equal(t, "Must pass all tests", e.AcceptanceCriteria)
				require.Equal(t, "Some notes", e.Notes)

				// Lifecycle fields.
				require.Equal(t, "eve", e.CreatedBy)
				require.NotNil(t, e.ClosedAt)
				require.True(t, e.ClosedAt.Equal(closedAt))
				require.Equal(t, "completed", e.CloseReason)
				require.Equal(t, "ses-abc", e.ClosedBySession)

				// ExternalRef.
				require.NotNil(t, e.ExternalRef)
				require.Equal(t, "gh-42", *e.ExternalRef)

				// EstimatedMinutes.
				require.NotNil(t, e.EstimatedMinutes)
				require.Equal(t, 60, *e.EstimatedMinutes)

				// Dependencies (combined from Dependencies + Dependents).
				require.Len(t, e.Dependencies, 2)
				require.Equal(t, "detail-1", e.Dependencies[0].IssueID)
				require.Equal(t, "blocker-1", e.Dependencies[0].DependsOnID)
				require.Equal(t, "child-1", e.Dependencies[1].IssueID)
				require.Equal(t, "detail-1", e.Dependencies[1].DependsOnID)

				// Comments.
				require.Len(t, e.Comments, 1)
				require.Equal(t, "frank", e.Comments[0].Author)
				require.Equal(t, "LGTM", e.Comments[0].Text)

				// Entity-only fields must be zero-valued.
				require.Empty(t, e.SourceSystem)
				require.Nil(t, e.DeletedAt)
				require.Empty(t, e.DeletedBy)
				require.Nil(t, e.Creator)
				require.Nil(t, e.Validations)
				require.Empty(t, e.AwaitType)
				require.Nil(t, e.BondedFrom)
				require.Empty(t, e.Holder)
				require.Empty(t, string(e.AgentState))
				require.Empty(t, string(e.MolType))
				require.Empty(t, string(e.WorkType))
				require.Empty(t, e.EventKind)
			},
		},
		{
			name: "Dependencies and Dependents combined into single entity.Issue.Dependencies list",
			input: backend.IssueDetailData{
				IssueData: backend.IssueData{ID: "combo-1"},
				Dependencies: []backend.DependencyData{
					{IssueID: "combo-1", DependsOnID: "dep-a", Type: "blocks", CreatedAt: now},
					{IssueID: "combo-1", DependsOnID: "dep-b", Type: "related", CreatedAt: now},
				},
				Dependents: []backend.DependencyData{
					{IssueID: "dep-c", DependsOnID: "combo-1", Type: "blocks", CreatedAt: now},
				},
			},
			verify: func(t *testing.T, e entity.Issue) {
				require.Len(t, e.Dependencies, 3)
				require.Equal(t, "dep-a", e.Dependencies[0].DependsOnID)
				require.Equal(t, "dep-b", e.Dependencies[1].DependsOnID)
				require.Equal(t, "dep-c", e.Dependencies[2].IssueID)
			},
		},
		{
			name: "empty ExternalRef produces nil entity.ExternalRef",
			input: backend.IssueDetailData{
				IssueData:   backend.IssueData{ID: "ext-empty"},
				ExternalRef: "",
			},
			verify: func(t *testing.T, e entity.Issue) {
				require.Nil(t, e.ExternalRef)
			},
		},
		{
			name: "non-empty ExternalRef gh-9 produces pointer",
			input: backend.IssueDetailData{
				IssueData:   backend.IssueData{ID: "ext-set"},
				ExternalRef: "gh-9",
			},
			verify: func(t *testing.T, e entity.Issue) {
				require.NotNil(t, e.ExternalRef)
				require.Equal(t, "gh-9", *e.ExternalRef)
			},
		},
		{
			name: "nil Dependencies/Dependents/Comments produce empty non-nil slices",
			input: backend.IssueDetailData{
				IssueData:    backend.IssueData{ID: "nil-rels"},
				Dependencies: nil,
				Dependents:   nil,
				Comments:     nil,
			},
			verify: func(t *testing.T, e entity.Issue) {
				require.NotNil(t, e.Dependencies)
				require.Empty(t, e.Dependencies)
				require.NotNil(t, e.Comments)
				require.Empty(t, e.Comments)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IssueFromDetailData(tt.input)
			tt.verify(t, got)
		})
	}
}

// ---------------------------------------------------------------------------
// IssueToData
// ---------------------------------------------------------------------------

func TestIssueToData(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	due := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	defer_ := time.Date(2026, 1, 20, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		input  entity.Issue
		verify func(t *testing.T, d backend.IssueData)
	}{
		{
			name: "fully populated entity.Issue maps shared fields, drops entity-only fields",
			input: entity.Issue{
				ID:         "ent-1",
				Title:      "Entity issue",
				Status:     entity.StatusInProgress,
				Priority:   3,
				IssueType:  entity.TypeFeature,
				Assignee:   "alice",
				Owner:      "bob",
				Labels:     []string{"frontend"},
				SourceRepo: "loomcli",
				CreatedAt:  now,
				UpdatedAt:  now.Add(time.Hour),
				DueAt:      &due,
				DeferUntil: &defer_,

				// Content fields (should be dropped).
				Description:        "long desc",
				Design:             "design doc",
				AcceptanceCriteria: "ac",
				Notes:              "notes",

				// Relational data (Dependencies count used).
				Dependencies: []*entity.Dependency{
					{IssueID: "ent-1", DependsOnID: "dep-1"},
					{IssueID: "ent-1", DependsOnID: "dep-2"},
					{IssueID: "ent-1", DependsOnID: "dep-3"},
				},
				Comments: []*entity.Comment{
					{ID: 1, IssueID: "ent-1", Author: "x", Text: "y"},
				},

				// Entity-only fields (should be silently dropped).
				Creator:        &entity.EntityRef{Name: "creator"},
				Crystallizes:   true,
				AwaitType:      "gate",
				AwaitID:        "g-1",
				Holder:         "holder-1",
				SourceFormula:  "formula",
				SourceLocation: "loc",
				HookBead:       "hook",
				RoleBead:       "role",
				AgentState:     entity.StateRunning,
				MolType:        entity.MolTypeSwarm,
				WorkType:       entity.WorkTypeMutex,
				EventKind:      "status",
				Actor:          "actor",
				Target:         "target",
				Payload:        "payload",
				Pinned:         true,
				IsTemplate:     true,
				Ephemeral:      true,
				Sender:         "sender",
				SourceSystem:   "github",
				BondedFrom:     []entity.BondRef{{SourceID: "src-1", BondType: "sequential"}},
			},
			verify: func(t *testing.T, d backend.IssueData) {
				require.Equal(t, "ent-1", d.ID)
				require.Equal(t, "Entity issue", d.Title)
				require.Equal(t, "in_progress", d.Status)
				require.Equal(t, 3, d.Priority)
				require.Equal(t, "feature", d.IssueType)
				require.Equal(t, "alice", d.Assignee)
				require.Equal(t, "bob", d.Owner)
				require.Equal(t, []string{"frontend"}, d.Labels)
				require.Equal(t, "loomcli", d.SourceRepo)
				require.True(t, d.CreatedAt.Equal(now))
				require.True(t, d.UpdatedAt.Equal(now.Add(time.Hour)))
				require.NotNil(t, d.DueAt)
				require.True(t, d.DueAt.Equal(due))
				require.NotNil(t, d.DeferUntil)
				require.True(t, d.DeferUntil.Equal(defer_))

				// DependencyCount from len(e.Dependencies).
				require.Equal(t, 3, d.DependencyCount)
				require.Equal(t, 0, d.DependentCount)

				// Parent is always empty.
				require.Empty(t, d.Parent)
			},
		},
		{
			name: "nil Labels in entity produces non-nil empty slice in IssueData",
			input: entity.Issue{
				Labels: nil,
			},
			verify: func(t *testing.T, d backend.IssueData) {
				require.NotNil(t, d.Labels)
				require.Empty(t, d.Labels)
			},
		},
		{
			name: "Dependencies set produces correct DependencyCount, DependentCount remains 0",
			input: entity.Issue{
				Dependencies: []*entity.Dependency{
					{IssueID: "a", DependsOnID: "b"},
					{IssueID: "a", DependsOnID: "c"},
				},
			},
			verify: func(t *testing.T, d backend.IssueData) {
				require.Equal(t, 2, d.DependencyCount)
				require.Equal(t, 0, d.DependentCount)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IssueToData(tt.input)
			tt.verify(t, got)
		})
	}
}

// ---------------------------------------------------------------------------
// IssuesFromData / IssuesToData (batch)
// ---------------------------------------------------------------------------

func TestIssuesFromData(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)

	t.Run("nil input produces non-nil empty output", func(t *testing.T) {
		result := IssuesFromData(nil)
		require.NotNil(t, result)
		require.Empty(t, result)
	})

	t.Run("empty input produces non-nil empty output", func(t *testing.T) {
		result := IssuesFromData([]backend.IssueData{})
		require.NotNil(t, result)
		require.Empty(t, result)
	})

	t.Run("multiple items mapped correctly", func(t *testing.T) {
		input := []backend.IssueData{
			{ID: "a", Title: "Alpha", CreatedAt: now, UpdatedAt: now},
			{ID: "b", Title: "Beta", CreatedAt: now, UpdatedAt: now},
			{ID: "c", Title: "Gamma", CreatedAt: now, UpdatedAt: now},
		}
		result := IssuesFromData(input)
		require.Len(t, result, 3)
		require.Equal(t, "a", result[0].ID)
		require.Equal(t, "b", result[1].ID)
		require.Equal(t, "c", result[2].ID)
	})
}

func TestIssuesToData(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)

	t.Run("nil input produces non-nil empty output", func(t *testing.T) {
		result := IssuesToData(nil)
		require.NotNil(t, result)
		require.Empty(t, result)
	})

	t.Run("empty input produces non-nil empty output", func(t *testing.T) {
		result := IssuesToData([]entity.Issue{})
		require.NotNil(t, result)
		require.Empty(t, result)
	})

	t.Run("multiple items mapped correctly", func(t *testing.T) {
		input := []entity.Issue{
			{ID: "x", Title: "X-ray", CreatedAt: now, UpdatedAt: now},
			{ID: "y", Title: "Yankee", CreatedAt: now, UpdatedAt: now},
		}
		result := IssuesToData(input)
		require.Len(t, result, 2)
		require.Equal(t, "x", result[0].ID)
		require.Equal(t, "y", result[1].ID)
	})
}

// ---------------------------------------------------------------------------
// Round-trip lossy test
// ---------------------------------------------------------------------------

func TestIssueRoundTrip_Lossy(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	due := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	defer_ := time.Date(2026, 1, 20, 8, 0, 0, 0, time.UTC)
	extRef := "gh-123"

	original := entity.Issue{
		// 13 shared fields.
		ID:         "round-1",
		Title:      "Round trip",
		Status:     entity.StatusInProgress,
		Priority:   2,
		IssueType:  entity.TypeFeature,
		Assignee:   "alice",
		Owner:      "bob",
		Labels:     []string{"api", "v2"},
		SourceRepo: "loomcli",
		CreatedAt:  now,
		UpdatedAt:  now.Add(time.Hour),
		DueAt:      &due,
		DeferUntil: &defer_,

		// Fields NOT in IssueData (should be lost).
		Description:        "desc",
		Design:             "design",
		AcceptanceCriteria: "ac",
		Notes:              "notes",
		CreatedBy:          "creator",
		ExternalRef:        &extRef,
		SourceSystem:       "github",
		Dependencies: []*entity.Dependency{
			{IssueID: "round-1", DependsOnID: "dep-1"},
		},
		Comments: []*entity.Comment{
			{ID: 1, IssueID: "round-1", Author: "x", Text: "y"},
		},
		Pinned:         true,
		IsTemplate:     true,
		Ephemeral:      true,
		Sender:         "sender",
		Creator:        &entity.EntityRef{Name: "hop-creator"},
		Crystallizes:   true,
		AwaitType:      "gate",
		AwaitID:        "g-1",
		Holder:         "holder",
		SourceFormula:  "formula",
		SourceLocation: "loc",
		HookBead:       "hook",
		RoleBead:       "role",
		AgentState:     entity.StateRunning,
		MolType:        entity.MolTypeSwarm,
		WorkType:       entity.WorkTypeMutex,
		EventKind:      "status",
		Actor:          "actor",
		Target:         "target",
		Payload:        "payload",
		BondedFrom:     []entity.BondRef{{SourceID: "src-1", BondType: "sequential"}},
	}

	// Round-trip: entity -> IssueData -> entity.
	data := IssueToData(original)
	roundTripped := IssueFromData(data)

	// The 13 shared fields must survive.
	require.Equal(t, original.ID, roundTripped.ID)
	require.Equal(t, original.Title, roundTripped.Title)
	require.Equal(t, original.Status, roundTripped.Status)
	require.Equal(t, original.Priority, roundTripped.Priority)
	require.Equal(t, original.IssueType, roundTripped.IssueType)
	require.Equal(t, original.Assignee, roundTripped.Assignee)
	require.Equal(t, original.Owner, roundTripped.Owner)
	require.Equal(t, original.Labels, roundTripped.Labels)
	require.Equal(t, original.SourceRepo, roundTripped.SourceRepo)
	require.True(t, original.CreatedAt.Equal(roundTripped.CreatedAt))
	require.True(t, original.UpdatedAt.Equal(roundTripped.UpdatedAt))
	require.NotNil(t, roundTripped.DueAt)
	require.True(t, original.DueAt.Equal(*roundTripped.DueAt))
	require.NotNil(t, roundTripped.DeferUntil)
	require.True(t, original.DeferUntil.Equal(*roundTripped.DeferUntil))

	// All entity-only fields must be zero-valued (lossy by design).
	require.Empty(t, roundTripped.Description)
	require.Empty(t, roundTripped.Design)
	require.Empty(t, roundTripped.AcceptanceCriteria)
	require.Empty(t, roundTripped.Notes)
	require.Empty(t, roundTripped.CreatedBy)
	require.Nil(t, roundTripped.ExternalRef)
	require.Empty(t, roundTripped.SourceSystem)
	require.NotNil(t, roundTripped.Dependencies)
	require.Empty(t, roundTripped.Dependencies)
	require.NotNil(t, roundTripped.Comments)
	require.Empty(t, roundTripped.Comments)
	require.False(t, roundTripped.Pinned)
	require.False(t, roundTripped.IsTemplate)
	require.False(t, roundTripped.Ephemeral)
	require.Empty(t, roundTripped.Sender)
	require.Nil(t, roundTripped.Creator)
	require.False(t, roundTripped.Crystallizes)
	require.Empty(t, roundTripped.AwaitType)
	require.Empty(t, roundTripped.AwaitID)
	require.Empty(t, roundTripped.Holder)
	require.Empty(t, roundTripped.SourceFormula)
	require.Empty(t, roundTripped.SourceLocation)
	require.Empty(t, roundTripped.HookBead)
	require.Empty(t, roundTripped.RoleBead)
	require.Empty(t, string(roundTripped.AgentState))
	require.Empty(t, string(roundTripped.MolType))
	require.Empty(t, string(roundTripped.WorkType))
	require.Empty(t, roundTripped.EventKind)
	require.Empty(t, roundTripped.Actor)
	require.Empty(t, roundTripped.Target)
	require.Empty(t, roundTripped.Payload)
	require.Nil(t, roundTripped.BondedFrom)
}
