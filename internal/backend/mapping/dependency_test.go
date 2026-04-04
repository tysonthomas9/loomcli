package mapping

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/entity"
)

// ---------------------------------------------------------------------------
// DependencyFromData
// ---------------------------------------------------------------------------

func TestDependencyFromData(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		name   string
		input  backend.DependencyData
		verify func(t *testing.T, d *entity.Dependency)
	}{
		{
			name: "fully populated DependencyData maps edge fields, drops inline display fields",
			input: backend.DependencyData{
				IssueID:     "issue-1",
				DependsOnID: "blocker-1",
				Type:        "blocks",
				Title:       "Blocker Title",
				Status:      "open",
				Priority:    2,
				IssueType:   "task",
				CreatedAt:   now,
				CreatedBy:   "alice",
			},
			verify: func(t *testing.T, d *entity.Dependency) {
				require.NotNil(t, d)
				require.Equal(t, "issue-1", d.IssueID)
				require.Equal(t, "blocker-1", d.DependsOnID)
				require.Equal(t, entity.DepBlocks, d.Type)
				require.True(t, d.CreatedAt.Equal(now))
				require.Equal(t, "alice", d.CreatedBy)

				// Inline display fields from DependencyData are dropped.
				// entity.Dependency has no Title, Status, Priority, IssueType fields.
				// Metadata and ThreadID are zero-valued.
				require.Empty(t, d.Metadata)
				require.Empty(t, d.ThreadID)
			},
		},
		{
			name: "zero-value CreatedAt is directly copied",
			input: backend.DependencyData{
				IssueID:     "a",
				DependsOnID: "b",
				Type:        "related",
			},
			verify: func(t *testing.T, d *entity.Dependency) {
				require.True(t, d.CreatedAt.IsZero())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DependencyFromData(tt.input)
			tt.verify(t, got)
		})
	}
}

// ---------------------------------------------------------------------------
// DependencyToData
// ---------------------------------------------------------------------------

func TestDependencyToData(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)

	t.Run("fully populated entity.Dependency maps edge fields, drops Metadata/ThreadID", func(t *testing.T) {
		dep := &entity.Dependency{
			IssueID:     "issue-1",
			DependsOnID: "dep-1",
			Type:        entity.DepBlocks,
			CreatedAt:   now,
			CreatedBy:   "bob",
			Metadata:    `{"gate":"all-children"}`,
			ThreadID:    "thread-42",
		}

		got := DependencyToData(dep)

		require.Equal(t, "issue-1", got.IssueID)
		require.Equal(t, "dep-1", got.DependsOnID)
		require.Equal(t, "blocks", got.Type)
		require.True(t, got.CreatedAt.Equal(now))
		require.Equal(t, "bob", got.CreatedBy)

		// Metadata and ThreadID are dropped.
		// Inline display fields are empty.
		require.Empty(t, got.Title)
		require.Empty(t, got.Status)
		require.Zero(t, got.Priority)
		require.Empty(t, got.IssueType)
	})

	t.Run("nil entity.Dependency returns zero-value DependencyData without panic", func(t *testing.T) {
		got := DependencyToData(nil)

		require.Empty(t, got.IssueID)
		require.Empty(t, got.DependsOnID)
		require.Empty(t, got.Type)
		require.True(t, got.CreatedAt.IsZero())
		require.Empty(t, got.CreatedBy)
	})
}

// ---------------------------------------------------------------------------
// DependenciesFromData / DependenciesToData (batch)
// ---------------------------------------------------------------------------

func TestDependenciesFromData(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)

	t.Run("nil input produces non-nil empty output", func(t *testing.T) {
		result := DependenciesFromData(nil)
		require.NotNil(t, result)
		require.Empty(t, result)
	})

	t.Run("multiple items mapped correctly", func(t *testing.T) {
		input := []backend.DependencyData{
			{IssueID: "a", DependsOnID: "b", Type: "blocks", CreatedAt: now},
			{IssueID: "c", DependsOnID: "d", Type: "related", CreatedAt: now},
		}
		result := DependenciesFromData(input)
		require.Len(t, result, 2)
		require.Equal(t, "a", result[0].IssueID)
		require.Equal(t, "c", result[1].IssueID)
	})
}

func TestDependenciesToData(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)

	t.Run("nil input produces non-nil empty output", func(t *testing.T) {
		result := DependenciesToData(nil)
		require.NotNil(t, result)
		require.Empty(t, result)
	})

	t.Run("input slice with nil entries produces zero-value outputs, not skipped", func(t *testing.T) {
		input := []*entity.Dependency{
			{IssueID: "a", DependsOnID: "b", Type: entity.DepBlocks, CreatedAt: now},
			nil,
			{IssueID: "c", DependsOnID: "d", Type: entity.DepRelated, CreatedAt: now},
		}
		result := DependenciesToData(input)
		require.Len(t, result, 3)

		// First entry mapped normally.
		require.Equal(t, "a", result[0].IssueID)
		require.Equal(t, "blocks", result[0].Type)

		// Second entry is zero-value from nil input (not skipped).
		require.Empty(t, result[1].IssueID)
		require.Empty(t, result[1].DependsOnID)
		require.Empty(t, result[1].Type)
		require.True(t, result[1].CreatedAt.IsZero())

		// Third entry mapped normally.
		require.Equal(t, "c", result[2].IssueID)
		require.Equal(t, "related", result[2].Type)
	})

	t.Run("multiple items correct count", func(t *testing.T) {
		input := []*entity.Dependency{
			{IssueID: "x", DependsOnID: "y", Type: entity.DepBlocks, CreatedAt: now},
			{IssueID: "y", DependsOnID: "z", Type: entity.DepRelated, CreatedAt: now},
		}
		result := DependenciesToData(input)
		require.Len(t, result, 2)
	})
}
