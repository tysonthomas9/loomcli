package types

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStatus_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status Status
		valid  bool
	}{
		{StatusOpen, true},
		{StatusInProgress, true},
		{StatusBlocked, true},
		{StatusDeferred, true},
		{StatusReview, true},
		{StatusClosed, true},
		{StatusTombstone, true},
		{StatusPinned, true},
		{StatusHooked, true},
		{"invalid", false},
		{"", false},
		{"OPEN", false}, // case sensitive
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.valid {
				t.Errorf("Status(%q).IsValid() = %v, want %v", tt.status, got, tt.valid)
			}
		})
	}
}

func TestStatus_IsValidWithCustom(t *testing.T) {
	t.Parallel()

	customStatuses := []string{"custom_status", "another_custom"}

	tests := []struct {
		status Status
		valid  bool
	}{
		{StatusOpen, true},
		{"custom_status", true},
		{"another_custom", true},
		{"invalid", false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsValidWithCustom(customStatuses); got != tt.valid {
				t.Errorf("Status(%q).IsValidWithCustom() = %v, want %v", tt.status, got, tt.valid)
			}
		})
	}
}

func TestIssueType_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		issueType IssueType
		valid     bool
	}{
		{TypeBug, true},
		{TypeFeature, true},
		{TypeTask, true},
		{TypeEpic, true},
		{TypeChore, true},
		{"invalid", false},
		{"", false},
		{"BUG", false}, // case sensitive
	}

	for _, tt := range tests {
		t.Run(string(tt.issueType), func(t *testing.T) {
			if got := tt.issueType.IsValid(); got != tt.valid {
				t.Errorf("IssueType(%q).IsValid() = %v, want %v", tt.issueType, got, tt.valid)
			}
		})
	}
}

func TestIssueType_Normalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    IssueType
		expected IssueType
	}{
		{"enhancement", TypeFeature},
		{"Enhancement", TypeFeature},
		{"ENHANCEMENT", TypeFeature},
		{"feat", TypeFeature},
		{"Feat", TypeFeature},
		{"bug", TypeBug},       // unchanged
		{"task", TypeTask},     // unchanged
		{"custom", "custom"},   // unchanged
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			if got := tt.input.Normalize(); got != tt.expected {
				t.Errorf("IssueType(%q).Normalize() = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestIssueType_RequiredSections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		issueType IssueType
		hasSteps  bool // bug has "Steps to Reproduce"
		hasAC     bool // bug, task, feature have "Acceptance Criteria"
		hasSC     bool // epic has "Success Criteria"
	}{
		{TypeBug, true, true, false},
		{TypeTask, false, true, false},
		{TypeFeature, false, true, false},
		{TypeEpic, false, false, true},
		{TypeChore, false, false, false},
		{"custom", false, false, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.issueType), func(t *testing.T) {
			sections := tt.issueType.RequiredSections()

			hasSteps := false
			hasAC := false
			hasSC := false
			for _, s := range sections {
				if strings.Contains(s.Heading, "Steps to Reproduce") {
					hasSteps = true
				}
				if strings.Contains(s.Heading, "Acceptance Criteria") {
					hasAC = true
				}
				if strings.Contains(s.Heading, "Success Criteria") {
					hasSC = true
				}
			}

			if hasSteps != tt.hasSteps || hasAC != tt.hasAC || hasSC != tt.hasSC {
				t.Errorf("IssueType(%q).RequiredSections() sections mismatch", tt.issueType)
			}
		})
	}
}

func TestAgentState_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state AgentState
		valid bool
	}{
		{StateIdle, true},
		{StateSpawning, true},
		{StateRunning, true},
		{StateWorking, true},
		{StateStuck, true},
		{StateDone, true},
		{StateStopped, true},
		{StateDead, true},
		{"", true}, // empty is valid for non-agent beads
		{"invalid", false},
	}

	for _, tt := range tests {
		name := string(tt.state)
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			if got := tt.state.IsValid(); got != tt.valid {
				t.Errorf("AgentState(%q).IsValid() = %v, want %v", tt.state, got, tt.valid)
			}
		})
	}
}

func TestMolType_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		molType MolType
		valid   bool
	}{
		{MolTypeSwarm, true},
		{MolTypePatrol, true},
		{MolTypeWork, true},
		{"", true}, // empty defaults to work
		{"invalid", false},
	}

	for _, tt := range tests {
		name := string(tt.molType)
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			if got := tt.molType.IsValid(); got != tt.valid {
				t.Errorf("MolType(%q).IsValid() = %v, want %v", tt.molType, got, tt.valid)
			}
		})
	}
}

func TestWorkType_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		workType WorkType
		valid    bool
	}{
		{WorkTypeMutex, true},
		{WorkTypeOpenCompetition, true},
		{"", true}, // empty defaults to mutex
		{"invalid", false},
	}

	for _, tt := range tests {
		name := string(tt.workType)
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			if got := tt.workType.IsValid(); got != tt.valid {
				t.Errorf("WorkType(%q).IsValid() = %v, want %v", tt.workType, got, tt.valid)
			}
		})
	}
}

func TestDependencyType_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		depType DependencyType
		valid   bool
	}{
		{DepBlocks, true},
		{DepParentChild, true},
		{"custom-type", true}, // valid: 1-50 chars
		{"", false},           // invalid: empty
		{DependencyType(strings.Repeat("x", 51)), false}, // invalid: >50 chars
	}

	for _, tt := range tests {
		name := string(tt.depType)
		if len(name) > 20 {
			name = name[:20] + "..."
		}
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			if got := tt.depType.IsValid(); got != tt.valid {
				t.Errorf("DependencyType(%q).IsValid() = %v, want %v", tt.depType, got, tt.valid)
			}
		})
	}
}

func TestDependencyType_IsWellKnown(t *testing.T) {
	t.Parallel()

	wellKnownTypes := []DependencyType{
		DepBlocks, DepParentChild, DepConditionalBlocks, DepWaitsFor,
		DepRelated, DepDiscoveredFrom, DepRepliesTo, DepRelatesTo,
		DepDuplicates, DepSupersedes, DepAuthoredBy, DepAssignedTo,
		DepApprovedBy, DepAttests, DepTracks, DepUntil, DepCausedBy,
		DepValidates, DepDelegatedFrom,
	}

	for _, dt := range wellKnownTypes {
		t.Run(string(dt), func(t *testing.T) {
			if !dt.IsWellKnown() {
				t.Errorf("DependencyType(%q).IsWellKnown() = false, want true", dt)
			}
		})
	}

	// Custom types should not be well-known
	t.Run("custom", func(t *testing.T) {
		custom := DependencyType("custom-type")
		if custom.IsWellKnown() {
			t.Errorf("DependencyType(%q).IsWellKnown() = true, want false", custom)
		}
	})
}

func TestDependencyType_AffectsReadyWork(t *testing.T) {
	t.Parallel()

	blockingTypes := []DependencyType{DepBlocks, DepParentChild, DepConditionalBlocks, DepWaitsFor}
	nonBlockingTypes := []DependencyType{DepRelated, DepRepliesTo, DepDuplicates, DepTracks}

	for _, dt := range blockingTypes {
		t.Run(string(dt)+"_blocks", func(t *testing.T) {
			if !dt.AffectsReadyWork() {
				t.Errorf("DependencyType(%q).AffectsReadyWork() = false, want true", dt)
			}
		})
	}

	for _, dt := range nonBlockingTypes {
		t.Run(string(dt)+"_non_blocking", func(t *testing.T) {
			if dt.AffectsReadyWork() {
				t.Errorf("DependencyType(%q).AffectsReadyWork() = true, want false", dt)
			}
		})
	}
}

func TestEventType_Constants(t *testing.T) {
	t.Parallel()

	expectedTypes := map[EventType]string{
		EventCreated:           "created",
		EventUpdated:           "updated",
		EventStatusChanged:     "status_changed",
		EventCommented:         "commented",
		EventClosed:            "closed",
		EventReopened:          "reopened",
		EventDependencyAdded:   "dependency_added",
		EventDependencyRemoved: "dependency_removed",
		EventLabelAdded:        "label_added",
		EventLabelRemoved:      "label_removed",
		EventCompacted:         "compacted",
	}

	for et, expected := range expectedTypes {
		if string(et) != expected {
			t.Errorf("EventType constant mismatch: got %q, want %q", et, expected)
		}
	}
}

func TestIssue_Validate(t *testing.T) {
	t.Parallel()

	now := time.Now()

	tests := []struct {
		name    string
		issue   Issue
		wantErr string
	}{
		{
			name: "valid minimal issue",
			issue: Issue{
				Title:     "Test issue",
				Status:    StatusOpen,
				IssueType: TypeTask,
				Priority:  2,
			},
			wantErr: "",
		},
		{
			name: "empty title",
			issue: Issue{
				Title: "",
			},
			wantErr: "title is required",
		},
		{
			name: "title too long",
			issue: Issue{
				Title: strings.Repeat("x", 501),
			},
			wantErr: "title must be 500 characters or less",
		},
		{
			name: "negative priority",
			issue: Issue{
				Title:    "Test",
				Priority: -1,
			},
			wantErr: "priority must be between 0 and 4",
		},
		{
			name: "priority too high",
			issue: Issue{
				Title:    "Test",
				Priority: 5,
			},
			wantErr: "priority must be between 0 and 4",
		},
		{
			name: "invalid status",
			issue: Issue{
				Title:  "Test",
				Status: "invalid",
			},
			wantErr: "invalid status",
		},
		{
			name: "invalid issue type",
			issue: Issue{
				Title:     "Test",
				Status:    StatusOpen,
				IssueType: "invalid",
			},
			wantErr: "invalid issue type",
		},
		{
			name: "negative estimated minutes",
			issue: Issue{
				Title:            "Test",
				Status:           StatusOpen,
				IssueType:        TypeTask,
				EstimatedMinutes: intPtr(-10),
			},
			wantErr: "estimated_minutes cannot be negative",
		},
		{
			name: "closed without closed_at",
			issue: Issue{
				Title:     "Test",
				Status:    StatusClosed,
				IssueType: TypeTask,
				ClosedAt:  nil,
			},
			wantErr: "closed issues must have closed_at timestamp",
		},
		{
			name: "non-closed with closed_at",
			issue: Issue{
				Title:     "Test",
				Status:    StatusOpen,
				IssueType: TypeTask,
				ClosedAt:  &now,
			},
			wantErr: "non-closed issues cannot have closed_at timestamp",
		},
		{
			name: "tombstone without deleted_at",
			issue: Issue{
				Title:     "Test",
				Status:    StatusTombstone,
				IssueType: TypeTask,
				DeletedAt: nil,
			},
			wantErr: "tombstone issues must have deleted_at timestamp",
		},
		{
			name: "non-tombstone with deleted_at",
			issue: Issue{
				Title:     "Test",
				Status:    StatusOpen,
				IssueType: TypeTask,
				DeletedAt: &now,
			},
			wantErr: "non-tombstone issues cannot have deleted_at timestamp",
		},
		{
			name: "invalid agent state",
			issue: Issue{
				Title:      "Test",
				Status:     StatusOpen,
				IssueType:  TypeTask,
				AgentState: "invalid_state",
			},
			wantErr: "invalid agent state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.issue.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("Validate() expected error containing %q, got nil", tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Validate() error = %q, want error containing %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestIssue_ValidateWithCustomStatuses(t *testing.T) {
	t.Parallel()

	customStatuses := []string{"custom_status"}

	issue := Issue{
		Title:     "Test",
		Status:    "custom_status",
		IssueType: TypeTask,
	}

	if err := issue.ValidateWithCustomStatuses(customStatuses); err != nil {
		t.Errorf("ValidateWithCustomStatuses() unexpected error: %v", err)
	}
}

func TestIssue_SetDefaults(t *testing.T) {
	t.Parallel()

	t.Run("empty status defaults to open", func(t *testing.T) {
		issue := Issue{}
		issue.SetDefaults()
		if issue.Status != StatusOpen {
			t.Errorf("SetDefaults() Status = %q, want %q", issue.Status, StatusOpen)
		}
	})

	t.Run("empty issue type defaults to task", func(t *testing.T) {
		issue := Issue{}
		issue.SetDefaults()
		if issue.IssueType != TypeTask {
			t.Errorf("SetDefaults() IssueType = %q, want %q", issue.IssueType, TypeTask)
		}
	})

	t.Run("priority 0 stays 0", func(t *testing.T) {
		issue := Issue{Priority: 0}
		issue.SetDefaults()
		if issue.Priority != 0 {
			t.Errorf("SetDefaults() Priority = %d, want 0", issue.Priority)
		}
	})

	t.Run("existing status not overwritten", func(t *testing.T) {
		issue := Issue{Status: StatusClosed}
		issue.SetDefaults()
		if issue.Status != StatusClosed {
			t.Errorf("SetDefaults() overwrote existing Status")
		}
	})
}

func TestIssue_IsTombstone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status     Status
		isTombstone bool
	}{
		{StatusTombstone, true},
		{StatusOpen, false},
		{StatusClosed, false},
		{StatusBlocked, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			issue := Issue{Status: tt.status}
			if got := issue.IsTombstone(); got != tt.isTombstone {
				t.Errorf("Issue.IsTombstone() = %v, want %v", got, tt.isTombstone)
			}
		})
	}
}

func TestIssue_IsExpired(t *testing.T) {
	t.Parallel()

	now := time.Now()
	oldTime := now.Add(-40 * 24 * time.Hour) // 40 days ago

	t.Run("non-tombstone never expires", func(t *testing.T) {
		issue := Issue{Status: StatusOpen}
		if issue.IsExpired(DefaultTombstoneTTL) {
			t.Error("Non-tombstone should never expire")
		}
	})

	t.Run("tombstone without DeletedAt is not expired", func(t *testing.T) {
		issue := Issue{Status: StatusTombstone, DeletedAt: nil}
		if issue.IsExpired(DefaultTombstoneTTL) {
			t.Error("Tombstone without DeletedAt should not be expired")
		}
	})

	t.Run("negative TTL means immediately expired", func(t *testing.T) {
		issue := Issue{Status: StatusTombstone, DeletedAt: &now}
		if !issue.IsExpired(-1) {
			t.Error("Negative TTL should mean immediately expired")
		}
	})

	t.Run("zero TTL uses default", func(t *testing.T) {
		issue := Issue{Status: StatusTombstone, DeletedAt: &oldTime}
		// 40 days old, default TTL is 30 days + 1 hour grace = expired
		if !issue.IsExpired(0) {
			t.Error("Zero TTL should use default and be expired for 40-day-old tombstone")
		}
	})

	t.Run("recent tombstone not expired", func(t *testing.T) {
		recentTime := now.Add(-1 * time.Hour)
		issue := Issue{Status: StatusTombstone, DeletedAt: &recentTime}
		if issue.IsExpired(DefaultTombstoneTTL) {
			t.Error("Recent tombstone should not be expired")
		}
	})

	t.Run("clock skew grace for long TTL", func(t *testing.T) {
		// Tombstone deleted 31 days ago with 30-day TTL + 1-hour grace
		deletedAt := now.Add(-31 * 24 * time.Hour)
		issue := Issue{Status: StatusTombstone, DeletedAt: &deletedAt}
		// Should be expired (31 days > 30 days + 1 hour)
		if !issue.IsExpired(30 * 24 * time.Hour) {
			t.Error("31-day-old tombstone should be expired with 30-day TTL")
		}
	})
}

func TestIssue_ComputeContentHash(t *testing.T) {
	t.Parallel()

	t.Run("determinism", func(t *testing.T) {
		issue := Issue{
			Title:       "Test Issue",
			Description: "Test description",
			Priority:    2,
		}
		h1 := issue.ComputeContentHash()
		h2 := issue.ComputeContentHash()
		if h1 != h2 {
			t.Errorf("ComputeContentHash() not deterministic: %s != %s", h1, h2)
		}
	})

	t.Run("different titles produce different hashes", func(t *testing.T) {
		issue1 := Issue{Title: "Issue A"}
		issue2 := Issue{Title: "Issue B"}
		if issue1.ComputeContentHash() == issue2.ComputeContentHash() {
			t.Error("Different titles should produce different hashes")
		}
	})

	t.Run("ignores ID field", func(t *testing.T) {
		issue1 := Issue{Title: "Test", ID: "bd-123"}
		issue2 := Issue{Title: "Test", ID: "bd-456"}
		if issue1.ComputeContentHash() != issue2.ComputeContentHash() {
			t.Error("ID should be ignored in content hash")
		}
	})

	t.Run("ignores timestamps", func(t *testing.T) {
		now := time.Now()
		later := now.Add(time.Hour)
		issue1 := Issue{Title: "Test", CreatedAt: now}
		issue2 := Issue{Title: "Test", CreatedAt: later}
		if issue1.ComputeContentHash() != issue2.ComputeContentHash() {
			t.Error("CreatedAt should be ignored in content hash")
		}
	})

	t.Run("ignores compaction metadata", func(t *testing.T) {
		now := time.Now()
		issue1 := Issue{Title: "Test", CompactionLevel: 0}
		issue2 := Issue{Title: "Test", CompactionLevel: 2, CompactedAt: &now}
		if issue1.ComputeContentHash() != issue2.ComputeContentHash() {
			t.Error("Compaction metadata should be ignored in content hash")
		}
	})
}

func TestIsFailureClose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		closeReason string
		isFailure   bool
	}{
		{"", false},
		{"completed", false},
		{"done", false},
		{"failed", true},
		{"Failed to deploy", true},
		{"rejected by reviewer", true},
		{"wontfix", true},
		{"won't fix", true},
		{"canceled", true},
		{"cancelled", true},
		{"abandoned", true},
		{"blocked by dependency", true},
		{"error during build", true},
		{"timeout", true},
		{"aborted", true},
		// Case insensitive
		{"FAILED", true},
		{"Rejected", true},
	}

	for _, tt := range tests {
		t.Run(tt.closeReason, func(t *testing.T) {
			if got := IsFailureClose(tt.closeReason); got != tt.isFailure {
				t.Errorf("IsFailureClose(%q) = %v, want %v", tt.closeReason, got, tt.isFailure)
			}
		})
	}
}

func TestEntityRef(t *testing.T) {
	t.Parallel()

	t.Run("URI generation", func(t *testing.T) {
		ref := EntityRef{
			Name:     "polecat/Nux",
			Platform: "gastown",
			Org:      "steveyegge",
			ID:       "polecat-nux",
		}
		expected := "entity://hop/gastown/steveyegge/polecat-nux"
		if got := ref.URI(); got != expected {
			t.Errorf("EntityRef.URI() = %q, want %q", got, expected)
		}
	})

	t.Run("URI with missing fields returns empty", func(t *testing.T) {
		ref := EntityRef{Platform: "gastown"}
		if got := ref.URI(); got != "" {
			t.Errorf("EntityRef.URI() with missing fields = %q, want empty", got)
		}
	})

	t.Run("IsEmpty", func(t *testing.T) {
		var nilRef *EntityRef
		if !nilRef.IsEmpty() {
			t.Error("nil EntityRef should be empty")
		}

		emptyRef := EntityRef{}
		if !emptyRef.IsEmpty() {
			t.Error("empty EntityRef should be empty")
		}

		nonEmpty := EntityRef{Name: "test"}
		if nonEmpty.IsEmpty() {
			t.Error("EntityRef with Name should not be empty")
		}
	})

	t.Run("String prefers Name", func(t *testing.T) {
		ref := EntityRef{
			Name:     "polecat/Nux",
			Platform: "gastown",
			Org:      "steveyegge",
			ID:       "polecat-nux",
		}
		if got := ref.String(); got != "polecat/Nux" {
			t.Errorf("EntityRef.String() = %q, want %q", got, "polecat/Nux")
		}
	})

	t.Run("round trip", func(t *testing.T) {
		original := EntityRef{
			Name:     "polecat/Nux",
			Platform: "gastown",
			Org:      "steveyegge",
			ID:       "polecat-nux",
		}
		uri := original.URI()
		parsed, err := ParseEntityURI(uri)
		if err != nil {
			t.Fatalf("ParseEntityURI() error: %v", err)
		}
		// Name is not preserved in URI
		if parsed.Platform != original.Platform || parsed.Org != original.Org || parsed.ID != original.ID {
			t.Errorf("Round trip failed: got %+v, want Platform=%s Org=%s ID=%s",
				parsed, original.Platform, original.Org, original.ID)
		}
	})
}

func TestParseEntityURI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		uri      string
		wantErr  bool
		platform string
		org      string
		id       string
	}{
		{"entity://hop/gastown/org/id", false, "gastown", "org", "id"},
		{"entity://hop/github/anthropics/claude", false, "github", "anthropics", "claude"},
		{"invalid", true, "", "", ""},
		{"entity://hop/incomplete", true, "", "", ""},
		{"entity://hop//org/id", true, "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			ref, err := ParseEntityURI(tt.uri)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseEntityURI(%q) expected error", tt.uri)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEntityURI(%q) error: %v", tt.uri, err)
			}
			if ref.Platform != tt.platform || ref.Org != tt.org || ref.ID != tt.id {
				t.Errorf("ParseEntityURI(%q) = %+v, want Platform=%s Org=%s ID=%s",
					tt.uri, ref, tt.platform, tt.org, tt.id)
			}
		})
	}
}

func TestValidation_IsValidOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		outcome string
		valid   bool
	}{
		{ValidationAccepted, true},
		{ValidationRejected, true},
		{ValidationRevisionRequested, true},
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.outcome, func(t *testing.T) {
			v := Validation{Outcome: tt.outcome}
			if got := v.IsValidOutcome(); got != tt.valid {
				t.Errorf("Validation.IsValidOutcome() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestBondRef_Constants(t *testing.T) {
	t.Parallel()

	expected := map[string]string{
		"sequential":  BondTypeSequential,
		"parallel":    BondTypeParallel,
		"conditional": BondTypeConditional,
		"root":        BondTypeRoot,
	}

	for want, got := range expected {
		if got != want {
			t.Errorf("BondType constant mismatch: got %q, want %q", got, want)
		}
	}
}

func TestSortPolicy_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		policy SortPolicy
		valid  bool
	}{
		{SortPolicyHybrid, true},
		{SortPolicyPriority, true},
		{SortPolicyOldest, true},
		{"", true}, // empty is valid (defaults)
		{"invalid", false},
	}

	for _, tt := range tests {
		name := string(tt.policy)
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			if got := tt.policy.IsValid(); got != tt.valid {
				t.Errorf("SortPolicy(%q).IsValid() = %v, want %v", tt.policy, got, tt.valid)
			}
		})
	}
}

func TestLabel_Comment_Struct(t *testing.T) {
	t.Parallel()

	t.Run("Label fields", func(t *testing.T) {
		label := Label{IssueID: "bd-123", Label: "bug"}
		if label.IssueID != "bd-123" || label.Label != "bug" {
			t.Error("Label struct fields not set correctly")
		}
	})

	t.Run("Comment fields", func(t *testing.T) {
		now := time.Now()
		comment := Comment{
			ID:        1,
			IssueID:   "bd-123",
			Author:    "alice",
			Text:      "test comment",
			CreatedAt: now,
		}
		if comment.ID != 1 || comment.IssueID != "bd-123" || comment.Author != "alice" {
			t.Error("Comment struct fields not set correctly")
		}
	})
}

func TestIssue_IsCompound_GetConstituents(t *testing.T) {
	t.Parallel()

	t.Run("non-compound", func(t *testing.T) {
		issue := Issue{Title: "Simple issue"}
		if issue.IsCompound() {
			t.Error("Issue without BondedFrom should not be compound")
		}
		if got := issue.GetConstituents(); got != nil {
			t.Errorf("GetConstituents() = %v, want nil", got)
		}
	})

	t.Run("compound", func(t *testing.T) {
		issue := Issue{
			Title: "Compound issue",
			BondedFrom: []BondRef{
				{SourceID: "bd-1", BondType: BondTypeSequential},
				{SourceID: "bd-2", BondType: BondTypeParallel},
			},
		}
		if !issue.IsCompound() {
			t.Error("Issue with BondedFrom should be compound")
		}
		constituents := issue.GetConstituents()
		if len(constituents) != 2 {
			t.Errorf("GetConstituents() returned %d items, want 2", len(constituents))
		}
	})
}

func TestIssue_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Millisecond)
	issue := Issue{
		ID:          "bd-test123",
		Title:       "Test Issue",
		Description: "Test description",
		Status:      StatusOpen,
		Priority:    2,
		IssueType:   TypeTask,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	data, err := json.Marshal(issue)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	var parsed Issue
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if parsed.ID != issue.ID || parsed.Title != issue.Title {
		t.Errorf("JSON round-trip failed: got ID=%s Title=%s, want ID=%s Title=%s",
			parsed.ID, parsed.Title, issue.ID, issue.Title)
	}
}

// Helper function
func intPtr(i int) *int {
	return &i
}
