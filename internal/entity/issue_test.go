package entity

import (
	"strings"
	"testing"
	"time"
)

func TestIssueStatus_IsValid(t *testing.T) {
	tests := []struct {
		status IssueStatus
		want   bool
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
		{"", true},
		{"unknown", false},
		{"OPEN", false},
		{"deleted", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.want {
				t.Errorf("IssueStatus(%q).IsValid() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestIssueStatus_IsValidWithCustom(t *testing.T) {
	tests := []struct {
		name           string
		status         IssueStatus
		customStatuses []string
		want           bool
	}{
		{"built-in status", StatusOpen, nil, true},
		{"custom status accepted", "waiting_qa", []string{"waiting_qa", "needs_info"}, true},
		{"custom status second", "needs_info", []string{"waiting_qa", "needs_info"}, true},
		{"unknown still invalid", "bogus", []string{"waiting_qa"}, false},
		{"empty with custom", "", []string{"waiting_qa"}, true},
		{"nil custom list", StatusOpen, nil, true},
		{"empty custom list", StatusOpen, []string{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsValidWithCustom(tt.customStatuses); got != tt.want {
				t.Errorf("IssueStatus(%q).IsValidWithCustom(%v) = %v, want %v",
					tt.status, tt.customStatuses, got, tt.want)
			}
		})
	}
}

func TestIssueType_IsValid(t *testing.T) {
	tests := []struct {
		issueType IssueType
		want      bool
	}{
		{TypeBug, true},
		{TypeFeature, true},
		{TypeTask, true},
		{TypeEpic, true},
		{TypeChore, true},
		{"", true},
		{"custom_type", false},
		{"BUG", false},
		{"enhancement", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.issueType), func(t *testing.T) {
			if got := tt.issueType.IsValid(); got != tt.want {
				t.Errorf("IssueType(%q).IsValid() = %v, want %v", tt.issueType, got, tt.want)
			}
		})
	}
}

func TestIssueType_IsBuiltIn(t *testing.T) {
	tests := []struct {
		issueType IssueType
		want      bool
	}{
		{TypeBug, true},
		{TypeFeature, true},
		{TypeTask, true},
		{TypeEpic, true},
		{TypeChore, true},
		{"", true},
		{"custom_type", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.issueType), func(t *testing.T) {
			if got := tt.issueType.IsBuiltIn(); got != tt.want {
				t.Errorf("IssueType(%q).IsBuiltIn() = %v, want %v", tt.issueType, got, tt.want)
			}
		})
	}
}

func TestIssueType_Normalize(t *testing.T) {
	tests := []struct {
		name  string
		input IssueType
		want  IssueType
	}{
		{"enhancement to feature", "enhancement", TypeFeature},
		{"feat to feature", "feat", TypeFeature},
		{"ENHANCEMENT case-insensitive", "ENHANCEMENT", TypeFeature},
		{"Feat case-insensitive", "Feat", TypeFeature},
		{"FEAT uppercase", "FEAT", TypeFeature},
		{"task passthrough", "task", TypeTask},
		{"bug passthrough", "bug", TypeBug},
		{"epic passthrough", "epic", TypeEpic},
		{"chore passthrough", "chore", TypeChore},
		{"feature passthrough", "feature", TypeFeature},
		{"empty passthrough", "", ""},
		{"unknown passthrough", "custom_type", "custom_type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.Normalize(); got != tt.want {
				t.Errorf("IssueType(%q).Normalize() = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIssueType_IsValidWithCustom(t *testing.T) {
	tests := []struct {
		name        string
		issueType   IssueType
		customTypes []string
		want        bool
	}{
		{"built-in type", TypeBug, nil, true},
		{"custom type accepted", "story", []string{"story", "spike"}, true},
		{"custom type second", "spike", []string{"story", "spike"}, true},
		{"unknown still invalid", "bogus", []string{"story"}, false},
		{"empty with custom", "", []string{"story"}, true},
		{"nil custom list", TypeTask, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.issueType.IsValidWithCustom(tt.customTypes); got != tt.want {
				t.Errorf("IssueType(%q).IsValidWithCustom(%v) = %v, want %v",
					tt.issueType, tt.customTypes, got, tt.want)
			}
		})
	}
}

func TestIssue_SetDefaults(t *testing.T) {
	t.Run("empty fields get defaults", func(t *testing.T) {
		i := &Issue{}
		i.SetDefaults()
		if i.Status != StatusOpen {
			t.Errorf("Status = %q, want %q", i.Status, StatusOpen)
		}
		if i.IssueType != TypeTask {
			t.Errorf("IssueType = %q, want %q", i.IssueType, TypeTask)
		}
	})

	t.Run("existing values preserved", func(t *testing.T) {
		i := &Issue{
			Status:    StatusBlocked,
			IssueType: TypeBug,
		}
		i.SetDefaults()
		if i.Status != StatusBlocked {
			t.Errorf("Status = %q, want %q", i.Status, StatusBlocked)
		}
		if i.IssueType != TypeBug {
			t.Errorf("IssueType = %q, want %q", i.IssueType, TypeBug)
		}
	})
}

func TestIssue_Validate(t *testing.T) {
	now := time.Now()
	validIssue := func() *Issue {
		return &Issue{
			Title:     "Fix login bug",
			Status:    StatusOpen,
			IssueType: TypeBug,
			Priority:  2,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}

	t.Run("valid issue passes", func(t *testing.T) {
		i := validIssue()
		if err := i.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty title fails", func(t *testing.T) {
		i := validIssue()
		i.Title = ""
		if err := i.Validate(); err == nil {
			t.Error("expected error for empty title")
		}
	})

	t.Run("title over 500 chars fails", func(t *testing.T) {
		i := validIssue()
		i.Title = strings.Repeat("a", 501)
		if err := i.Validate(); err == nil {
			t.Error("expected error for title > 500 chars")
		}
	})

	t.Run("title exactly 500 chars passes", func(t *testing.T) {
		i := validIssue()
		i.Title = strings.Repeat("a", 500)
		if err := i.Validate(); err != nil {
			t.Errorf("unexpected error for 500-char title: %v", err)
		}
	})

	t.Run("priority -1 fails", func(t *testing.T) {
		i := validIssue()
		i.Priority = -1
		if err := i.Validate(); err == nil {
			t.Error("expected error for priority -1")
		}
	})

	t.Run("priority 5 fails", func(t *testing.T) {
		i := validIssue()
		i.Priority = 5
		if err := i.Validate(); err == nil {
			t.Error("expected error for priority 5")
		}
	})

	t.Run("priority 0 passes", func(t *testing.T) {
		i := validIssue()
		i.Priority = 0
		if err := i.Validate(); err != nil {
			t.Errorf("unexpected error for priority 0: %v", err)
		}
	})

	t.Run("priority 4 passes", func(t *testing.T) {
		i := validIssue()
		i.Priority = 4
		if err := i.Validate(); err != nil {
			t.Errorf("unexpected error for priority 4: %v", err)
		}
	})

	t.Run("invalid status fails", func(t *testing.T) {
		i := validIssue()
		i.Status = "invalid_status"
		if err := i.Validate(); err == nil {
			t.Error("expected error for invalid status")
		}
	})

	t.Run("closed without ClosedAt fails", func(t *testing.T) {
		i := validIssue()
		i.Status = StatusClosed
		i.ClosedAt = nil
		if err := i.Validate(); err == nil {
			t.Error("expected error for closed issue without closed_at")
		}
	})

	t.Run("closed with ClosedAt passes", func(t *testing.T) {
		i := validIssue()
		i.Status = StatusClosed
		i.ClosedAt = &now
		if err := i.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("non-closed with ClosedAt fails", func(t *testing.T) {
		i := validIssue()
		i.Status = StatusOpen
		i.ClosedAt = &now
		if err := i.Validate(); err == nil {
			t.Error("expected error for non-closed issue with closed_at")
		}
	})

	t.Run("tombstone without DeletedAt fails", func(t *testing.T) {
		i := validIssue()
		i.Status = StatusTombstone
		i.DeletedAt = nil
		if err := i.Validate(); err == nil {
			t.Error("expected error for tombstone without deleted_at")
		}
	})

	t.Run("tombstone with DeletedAt and ClosedAt passes", func(t *testing.T) {
		i := validIssue()
		i.Status = StatusTombstone
		i.DeletedAt = &now
		i.ClosedAt = &now
		if err := i.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("non-tombstone with DeletedAt fails", func(t *testing.T) {
		i := validIssue()
		i.Status = StatusOpen
		i.DeletedAt = &now
		if err := i.Validate(); err == nil {
			t.Error("expected error for non-tombstone with deleted_at")
		}
	})

	t.Run("negative EstimatedMinutes fails", func(t *testing.T) {
		i := validIssue()
		neg := -1
		i.EstimatedMinutes = &neg
		if err := i.Validate(); err == nil {
			t.Error("expected error for negative estimated_minutes")
		}
	})

	t.Run("zero EstimatedMinutes passes", func(t *testing.T) {
		i := validIssue()
		zero := 0
		i.EstimatedMinutes = &zero
		if err := i.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("invalid AgentState fails", func(t *testing.T) {
		i := validIssue()
		i.AgentState = "bad_state"
		if err := i.Validate(); err == nil {
			t.Error("expected error for invalid agent state")
		}
	})

	t.Run("valid AgentState passes", func(t *testing.T) {
		i := validIssue()
		i.AgentState = StateRunning
		if err := i.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestIssue_ValidateWithCustom(t *testing.T) {
	now := time.Now()

	t.Run("custom status accepted", func(t *testing.T) {
		i := &Issue{
			Title:     "Test issue",
			Status:    "waiting_qa",
			IssueType: TypeTask,
			CreatedAt: now,
			UpdatedAt: now,
		}
		err := i.ValidateWithCustom([]string{"waiting_qa"}, nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("custom type accepted", func(t *testing.T) {
		i := &Issue{
			Title:     "Test issue",
			Status:    StatusOpen,
			IssueType: "story",
			CreatedAt: now,
			UpdatedAt: now,
		}
		err := i.ValidateWithCustom(nil, []string{"story"})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("unknown custom status rejected", func(t *testing.T) {
		i := &Issue{
			Title:     "Test issue",
			Status:    "bad_status",
			IssueType: TypeTask,
			CreatedAt: now,
			UpdatedAt: now,
		}
		err := i.ValidateWithCustom([]string{"waiting_qa"}, nil)
		if err == nil {
			t.Error("expected error for unknown custom status")
		}
	})

	t.Run("unknown custom type rejected", func(t *testing.T) {
		i := &Issue{
			Title:     "Test issue",
			Status:    StatusOpen,
			IssueType: "bad_type",
			CreatedAt: now,
			UpdatedAt: now,
		}
		err := i.ValidateWithCustom(nil, []string{"story"})
		if err == nil {
			t.Error("expected error for unknown custom type")
		}
	})
}

func TestIssue_IsTombstone(t *testing.T) {
	tests := []struct {
		name   string
		status IssueStatus
		want   bool
	}{
		{"tombstone status", StatusTombstone, true},
		{"open status", StatusOpen, false},
		{"closed status", StatusClosed, false},
		{"empty status", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := &Issue{Status: tt.status}
			if got := i.IsTombstone(); got != tt.want {
				t.Errorf("IsTombstone() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIssue_IsExpired(t *testing.T) {
	t.Run("non-tombstone returns false", func(t *testing.T) {
		i := &Issue{Status: StatusOpen}
		if i.IsExpired(DefaultTombstoneTTL) {
			t.Error("non-tombstone should not be expired")
		}
	})

	t.Run("tombstone without DeletedAt returns false", func(t *testing.T) {
		i := &Issue{Status: StatusTombstone}
		if i.IsExpired(DefaultTombstoneTTL) {
			t.Error("tombstone without DeletedAt should not be expired")
		}
	})

	t.Run("tombstone within TTL returns false", func(t *testing.T) {
		recent := time.Now().Add(-1 * time.Hour)
		i := &Issue{
			Status:    StatusTombstone,
			DeletedAt: &recent,
		}
		if i.IsExpired(DefaultTombstoneTTL) {
			t.Error("recently deleted tombstone should not be expired")
		}
	})

	t.Run("tombstone past TTL returns true", func(t *testing.T) {
		// Use a time well past the default TTL + clock skew grace
		old := time.Now().Add(-(DefaultTombstoneTTL + ClockSkewGrace + time.Hour))
		i := &Issue{
			Status:    StatusTombstone,
			DeletedAt: &old,
		}
		if !i.IsExpired(DefaultTombstoneTTL) {
			t.Error("old tombstone should be expired")
		}
	})

	t.Run("zero TTL uses default", func(t *testing.T) {
		recent := time.Now().Add(-1 * time.Hour)
		i := &Issue{
			Status:    StatusTombstone,
			DeletedAt: &recent,
		}
		if i.IsExpired(0) {
			t.Error("should use DefaultTombstoneTTL when ttl=0, recent tombstone should not be expired")
		}
	})

	t.Run("negative TTL immediately expired", func(t *testing.T) {
		recent := time.Now()
		i := &Issue{
			Status:    StatusTombstone,
			DeletedAt: &recent,
		}
		if !i.IsExpired(-1) {
			t.Error("negative TTL should immediately expire tombstone")
		}
	})
}

func TestIssue_IsCompound(t *testing.T) {
	t.Run("has BondedFrom", func(t *testing.T) {
		i := &Issue{
			BondedFrom: []BondRef{
				{SourceID: "src-1", BondType: BondTypeSequential},
			},
		}
		if !i.IsCompound() {
			t.Error("issue with BondedFrom should be compound")
		}
	})

	t.Run("empty BondedFrom", func(t *testing.T) {
		i := &Issue{}
		if i.IsCompound() {
			t.Error("issue without BondedFrom should not be compound")
		}
	})

	t.Run("nil BondedFrom", func(t *testing.T) {
		i := &Issue{BondedFrom: nil}
		if i.IsCompound() {
			t.Error("issue with nil BondedFrom should not be compound")
		}
	})
}

func TestIssue_GetConstituents(t *testing.T) {
	t.Run("returns BondedFrom for compound", func(t *testing.T) {
		refs := []BondRef{
			{SourceID: "src-1", BondType: BondTypeSequential},
			{SourceID: "src-2", BondType: BondTypeParallel},
		}
		i := &Issue{BondedFrom: refs}
		got := i.GetConstituents()
		if len(got) != 2 {
			t.Fatalf("GetConstituents() returned %d refs, want 2", len(got))
		}
		if got[0].SourceID != "src-1" || got[1].SourceID != "src-2" {
			t.Errorf("unexpected constituents: %+v", got)
		}
	})

	t.Run("returns nil for non-compound", func(t *testing.T) {
		i := &Issue{}
		got := i.GetConstituents()
		if got != nil {
			t.Errorf("GetConstituents() = %v, want nil", got)
		}
	})
}

func TestEntityRef_URI(t *testing.T) {
	t.Run("all fields set", func(t *testing.T) {
		ref := &EntityRef{
			Name:     "Alice",
			Platform: "github",
			Org:      "acme",
			ID:       "alice123",
		}
		want := "entity://hop/github/acme/alice123"
		if got := ref.URI(); got != want {
			t.Errorf("URI() = %q, want %q", got, want)
		}
	})

	t.Run("missing Platform returns empty", func(t *testing.T) {
		ref := &EntityRef{Org: "acme", ID: "alice123"}
		if got := ref.URI(); got != "" {
			t.Errorf("URI() = %q, want empty", got)
		}
	})

	t.Run("missing Org returns empty", func(t *testing.T) {
		ref := &EntityRef{Platform: "github", ID: "alice123"}
		if got := ref.URI(); got != "" {
			t.Errorf("URI() = %q, want empty", got)
		}
	})

	t.Run("missing ID returns empty", func(t *testing.T) {
		ref := &EntityRef{Platform: "github", Org: "acme"}
		if got := ref.URI(); got != "" {
			t.Errorf("URI() = %q, want empty", got)
		}
	})

	t.Run("nil receiver returns empty", func(t *testing.T) {
		var ref *EntityRef
		if got := ref.URI(); got != "" {
			t.Errorf("URI() = %q, want empty", got)
		}
	})
}

func TestEntityRef_IsEmpty(t *testing.T) {
	t.Run("nil is empty", func(t *testing.T) {
		var ref *EntityRef
		if !ref.IsEmpty() {
			t.Error("nil EntityRef should be empty")
		}
	})

	t.Run("zero value is empty", func(t *testing.T) {
		ref := &EntityRef{}
		if !ref.IsEmpty() {
			t.Error("zero-value EntityRef should be empty")
		}
	})

	t.Run("name only is not empty", func(t *testing.T) {
		ref := &EntityRef{Name: "Alice"}
		if ref.IsEmpty() {
			t.Error("EntityRef with Name should not be empty")
		}
	})

	t.Run("ID only is not empty", func(t *testing.T) {
		ref := &EntityRef{ID: "abc"}
		if ref.IsEmpty() {
			t.Error("EntityRef with ID should not be empty")
		}
	})
}

func TestEntityRef_String(t *testing.T) {
	t.Run("nil returns empty", func(t *testing.T) {
		var ref *EntityRef
		if got := ref.String(); got != "" {
			t.Errorf("String() = %q, want empty", got)
		}
	})

	t.Run("prefers Name", func(t *testing.T) {
		ref := &EntityRef{
			Name:     "Alice",
			Platform: "github",
			Org:      "acme",
			ID:       "alice123",
		}
		if got := ref.String(); got != "Alice" {
			t.Errorf("String() = %q, want %q", got, "Alice")
		}
	})

	t.Run("falls back to URI", func(t *testing.T) {
		ref := &EntityRef{
			Platform: "github",
			Org:      "acme",
			ID:       "alice123",
		}
		want := "entity://hop/github/acme/alice123"
		if got := ref.String(); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to ID", func(t *testing.T) {
		ref := &EntityRef{ID: "alice123"}
		if got := ref.String(); got != "alice123" {
			t.Errorf("String() = %q, want %q", got, "alice123")
		}
	})

	t.Run("empty ref returns empty", func(t *testing.T) {
		ref := &EntityRef{}
		if got := ref.String(); got != "" {
			t.Errorf("String() = %q, want empty", got)
		}
	})
}

func TestParseEntityURI(t *testing.T) {
	t.Run("valid URI", func(t *testing.T) {
		ref, err := ParseEntityURI("entity://hop/github/acme/alice123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ref.Platform != "github" {
			t.Errorf("Platform = %q, want %q", ref.Platform, "github")
		}
		if ref.Org != "acme" {
			t.Errorf("Org = %q, want %q", ref.Org, "acme")
		}
		if ref.ID != "alice123" {
			t.Errorf("ID = %q, want %q", ref.ID, "alice123")
		}
	})

	t.Run("valid URI with slashes in ID", func(t *testing.T) {
		ref, err := ParseEntityURI("entity://hop/github/acme/teams/backend")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ref.Platform != "github" {
			t.Errorf("Platform = %q, want %q", ref.Platform, "github")
		}
		if ref.Org != "acme" {
			t.Errorf("Org = %q, want %q", ref.Org, "acme")
		}
		if ref.ID != "teams/backend" {
			t.Errorf("ID = %q, want %q", ref.ID, "teams/backend")
		}
	})

	t.Run("invalid prefix", func(t *testing.T) {
		_, err := ParseEntityURI("http://example.com/foo")
		if err == nil {
			t.Error("expected error for invalid prefix")
		}
	})

	t.Run("too few parts", func(t *testing.T) {
		_, err := ParseEntityURI("entity://hop/github/acme")
		if err == nil {
			t.Error("expected error for too few parts")
		}
	})

	t.Run("empty platform", func(t *testing.T) {
		_, err := ParseEntityURI("entity://hop//acme/alice")
		if err == nil {
			t.Error("expected error for empty platform")
		}
	})

	t.Run("empty org", func(t *testing.T) {
		_, err := ParseEntityURI("entity://hop/github//alice")
		if err == nil {
			t.Error("expected error for empty org")
		}
	})
}

func TestAgentState_IsValid(t *testing.T) {
	tests := []struct {
		state AgentState
		want  bool
	}{
		{StateIdle, true},
		{StateSpawning, true},
		{StateRunning, true},
		{StateWorking, true},
		{StateStuck, true},
		{StateDone, true},
		{StateStopped, true},
		{StateDead, true},
		{"", true},
		{"unknown", false},
		{"IDLE", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			if got := tt.state.IsValid(); got != tt.want {
				t.Errorf("AgentState(%q).IsValid() = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

func TestMolType_IsValid(t *testing.T) {
	tests := []struct {
		molType MolType
		want    bool
	}{
		{MolTypeSwarm, true},
		{MolTypePatrol, true},
		{MolTypeWork, true},
		{"", true},
		{"unknown", false},
		{"SWARM", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.molType), func(t *testing.T) {
			if got := tt.molType.IsValid(); got != tt.want {
				t.Errorf("MolType(%q).IsValid() = %v, want %v", tt.molType, got, tt.want)
			}
		})
	}
}

func TestWorkType_IsValid(t *testing.T) {
	tests := []struct {
		workType WorkType
		want     bool
	}{
		{WorkTypeMutex, true},
		{WorkTypeOpenCompetition, true},
		{"", true},
		{"unknown", false},
		{"MUTEX", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.workType), func(t *testing.T) {
			if got := tt.workType.IsValid(); got != tt.want {
				t.Errorf("WorkType(%q).IsValid() = %v, want %v", tt.workType, got, tt.want)
			}
		})
	}
}

func TestValidation_IsValidOutcome(t *testing.T) {
	tests := []struct {
		outcome string
		want    bool
	}{
		{ValidationAccepted, true},
		{ValidationRejected, true},
		{ValidationRevisionRequested, true},
		{"unknown", false},
		{"", false},
		{"ACCEPTED", false},
	}
	for _, tt := range tests {
		t.Run(tt.outcome, func(t *testing.T) {
			v := &Validation{Outcome: tt.outcome}
			if got := v.IsValidOutcome(); got != tt.want {
				t.Errorf("Validation{Outcome: %q}.IsValidOutcome() = %v, want %v",
					tt.outcome, got, tt.want)
			}
		})
	}
}
