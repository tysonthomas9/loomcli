package types

import (
	"testing"
	"time"
)

func TestValidateForImport_ValidBuiltInType(t *testing.T) {
	t.Parallel()

	builtInTypes := []IssueType{TypeBug, TypeFeature, TypeTask, TypeEpic, TypeChore}

	for _, issueType := range builtInTypes {
		t.Run(string(issueType), func(t *testing.T) {
			issue := &Issue{
				Title:     "Test issue",
				Status:    StatusOpen,
				IssueType: issueType,
			}
			if err := issue.ValidateForImport(nil); err != nil {
				t.Errorf("ValidateForImport() with built-in type %q = %v, want nil", issueType, err)
			}
		})
	}
}

func TestValidateForImport_CustomType(t *testing.T) {
	t.Parallel()

	// Non-built-in types should be accepted per federation trust model
	customTypes := []IssueType{"molecule", "gate", "custom", "story", "incident"}

	for _, issueType := range customTypes {
		t.Run(string(issueType), func(t *testing.T) {
			issue := &Issue{
				Title:     "Test issue",
				Status:    StatusOpen,
				IssueType: issueType,
			}
			if err := issue.ValidateForImport(nil); err != nil {
				t.Errorf("ValidateForImport() with custom type %q = %v, want nil (federation trust model)", issueType, err)
			}
		})
	}
}

func TestValidateForImport_EmptyType(t *testing.T) {
	t.Parallel()

	// Empty issue type should pass - SetDefaults() assigns TypeTask post-import
	issue := &Issue{
		Title:     "Test issue",
		Status:    StatusOpen,
		IssueType: "",
	}
	if err := issue.ValidateForImport(nil); err != nil {
		t.Errorf("ValidateForImport() with empty type = %v, want nil", err)
	}
}

func TestValidateForImport_TitleRequired(t *testing.T) {
	t.Parallel()

	issue := &Issue{
		Title:     "",
		Status:    StatusOpen,
		IssueType: TypeTask,
	}
	err := issue.ValidateForImport(nil)
	if err == nil {
		t.Error("ValidateForImport() with empty title = nil, want error")
	}
}

func TestValidateForImport_TitleTooLong(t *testing.T) {
	t.Parallel()

	longTitle := make([]byte, 501)
	for i := range longTitle {
		longTitle[i] = 'a'
	}

	issue := &Issue{
		Title:     string(longTitle),
		Status:    StatusOpen,
		IssueType: TypeTask,
	}
	err := issue.ValidateForImport(nil)
	if err == nil {
		t.Error("ValidateForImport() with title > 500 chars = nil, want error")
	}
}

func TestValidateForImport_TitleAtBoundary(t *testing.T) {
	t.Parallel()

	// Exactly 500 chars should pass
	boundaryTitle := make([]byte, 500)
	for i := range boundaryTitle {
		boundaryTitle[i] = 'a'
	}

	issue := &Issue{
		Title:     string(boundaryTitle),
		Status:    StatusOpen,
		IssueType: TypeTask,
	}
	err := issue.ValidateForImport(nil)
	if err != nil {
		t.Errorf("ValidateForImport() with title exactly 500 chars = %v, want nil", err)
	}
}

func TestValidateForImport_PriorityRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		priority  int
		wantError bool
	}{
		{"P0 valid", 0, false},
		{"P1 valid", 1, false},
		{"P2 valid", 2, false},
		{"P3 valid", 3, false},
		{"P4 valid", 4, false},
		{"negative invalid", -1, true},
		{"P5 invalid", 5, true},
		{"large invalid", 100, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := &Issue{
				Title:    "Test issue",
				Status:   StatusOpen,
				Priority: tt.priority,
			}
			err := issue.ValidateForImport(nil)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateForImport() with priority %d: error = %v, wantError = %v", tt.priority, err, tt.wantError)
			}
		})
	}
}

func TestValidateForImport_CustomStatus(t *testing.T) {
	t.Parallel()

	customStatuses := []string{"needs_review", "waiting_on_customer"}

	tests := []struct {
		name      string
		status    Status
		wantError bool
	}{
		{"built-in open", StatusOpen, false},
		{"built-in closed", StatusClosed, false},
		{"custom needs_review", "needs_review", false},
		{"custom waiting_on_customer", "waiting_on_customer", false},
		{"unknown status", "totally_unknown", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := &Issue{
				Title:  "Test issue",
				Status: tt.status,
			}
			// Add closed_at for closed status
			if tt.status == StatusClosed {
				now := time.Now()
				issue.ClosedAt = &now
			}
			err := issue.ValidateForImport(customStatuses)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateForImport() with status %q: error = %v, wantError = %v", tt.status, err, tt.wantError)
			}
		})
	}
}

func TestValidateForImport_InvalidStatus(t *testing.T) {
	t.Parallel()

	// Unknown status without custom list should fail
	issue := &Issue{
		Title:  "Test issue",
		Status: "totally_unknown",
	}
	err := issue.ValidateForImport(nil)
	if err == nil {
		t.Error("ValidateForImport() with unknown status and no custom list = nil, want error")
	}
}

func TestValidateForImport_ClosedAtInvariant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    Status
		closedAt  *time.Time
		wantError bool
	}{
		{"closed with timestamp", StatusClosed, timePtr(time.Now()), false},
		{"closed without timestamp", StatusClosed, nil, true},
		{"open with timestamp", StatusOpen, timePtr(time.Now()), true},
		{"open without timestamp", StatusOpen, nil, false},
		{"in_progress with timestamp", StatusInProgress, timePtr(time.Now()), true},
		{"in_progress without timestamp", StatusInProgress, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := &Issue{
				Title:    "Test issue",
				Status:   tt.status,
				ClosedAt: tt.closedAt,
			}
			err := issue.ValidateForImport(nil)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateForImport() %s: error = %v, wantError = %v", tt.name, err, tt.wantError)
			}
		})
	}
}

func TestValidateForImport_TombstoneInvariant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    Status
		deletedAt *time.Time
		wantError bool
	}{
		{"tombstone with timestamp", StatusTombstone, timePtr(time.Now()), false},
		{"tombstone without timestamp", StatusTombstone, nil, true},
		{"open with deleted_at", StatusOpen, timePtr(time.Now()), true},
		{"open without deleted_at", StatusOpen, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := &Issue{
				Title:     "Test issue",
				Status:    tt.status,
				DeletedAt: tt.deletedAt,
			}
			err := issue.ValidateForImport(nil)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateForImport() %s: error = %v, wantError = %v", tt.name, err, tt.wantError)
			}
		})
	}
}

func TestValidateForImport_AgentStateValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		agentState AgentState
		wantError  bool
	}{
		{"empty state", "", false},
		{"idle", StateIdle, false},
		{"spawning", StateSpawning, false},
		{"running", StateRunning, false},
		{"working", StateWorking, false},
		{"stuck", StateStuck, false},
		{"done", StateDone, false},
		{"stopped", StateStopped, false},
		{"dead", StateDead, false},
		{"invalid state", "invalid_state", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := &Issue{
				Title:      "Test issue",
				Status:     StatusOpen,
				AgentState: tt.agentState,
			}
			err := issue.ValidateForImport(nil)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateForImport() with agent state %q: error = %v, wantError = %v", tt.agentState, err, tt.wantError)
			}
		})
	}
}

func TestValidateForImport_EstimatedMinutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		minutes   *int
		wantError bool
	}{
		{"nil", nil, false},
		{"zero", intPtr(0), false},
		{"positive", intPtr(60), false},
		{"negative", intPtr(-1), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := &Issue{
				Title:            "Test issue",
				Status:           StatusOpen,
				EstimatedMinutes: tt.minutes,
			}
			err := issue.ValidateForImport(nil)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateForImport() with estimated_minutes %v: error = %v, wantError = %v", tt.minutes, err, tt.wantError)
			}
		})
	}
}
