package dto

import (
	"errors"
	"strings"
	"testing"
)

func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }

// validCreate returns a minimal valid CreateIssueRequest.
func validCreate() CreateIssueRequest {
	return CreateIssueRequest{
		Title:     "Test issue",
		IssueType: "task",
		Priority:  2,
	}
}

// --- CreateIssueRequest tests ---

func TestCreateIssueRequest_Validate_Valid(t *testing.T) {
	r := validCreate()
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestCreateIssueRequest_Validate_ValidAllFields(t *testing.T) {
	r := CreateIssueRequest{
		Title:            "Full issue",
		IssueType:        "bug",
		Priority:         0,
		Labels:           []string{"urgent"},
		Dependencies:     []string{"dep-1"},
		EstimatedMinutes: intPtr(60),
		DueAt:            "2024-01-15T10:30:00Z",
		DeferUntil:       "2024-02-01T00:00:00+05:00",
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestCreateIssueRequest_Validate_EmptyTitle(t *testing.T) {
	r := validCreate()
	r.Title = ""
	assertFieldError(t, r.Validate(), "title", "is required")
}

func TestCreateIssueRequest_Validate_WhitespaceTitle(t *testing.T) {
	r := validCreate()
	r.Title = "   "
	assertFieldError(t, r.Validate(), "title", "is required")
}

func TestCreateIssueRequest_Validate_TitleTooLong(t *testing.T) {
	r := validCreate()
	r.Title = strings.Repeat("a", MaxTitleLength+1)
	assertFieldError(t, r.Validate(), "title", "must be 500 characters or less")
}

func TestCreateIssueRequest_Validate_TitleExactly500(t *testing.T) {
	r := validCreate()
	r.Title = strings.Repeat("a", MaxTitleLength)
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for exactly %d chars", err, MaxTitleLength)
	}
}

func TestCreateIssueRequest_Validate_EmptyIssueType(t *testing.T) {
	r := validCreate()
	r.IssueType = ""
	assertFieldError(t, r.Validate(), "issue_type", "is required")
}

func TestCreateIssueRequest_Validate_InvalidIssueType(t *testing.T) {
	r := validCreate()
	r.IssueType = "widget"
	assertFieldError(t, r.Validate(), "issue_type", "must be one of")
}

func TestCreateIssueRequest_Validate_PriorityNegative(t *testing.T) {
	r := validCreate()
	r.Priority = -1
	assertFieldError(t, r.Validate(), "priority", "must be between 0 and 4")
}

func TestCreateIssueRequest_Validate_PriorityTooHigh(t *testing.T) {
	r := validCreate()
	r.Priority = 5
	assertFieldError(t, r.Validate(), "priority", "must be between 0 and 4")
}

func TestCreateIssueRequest_Validate_PriorityZero(t *testing.T) {
	r := validCreate()
	r.Priority = 0
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for P0", err)
	}
}

func TestCreateIssueRequest_Validate_PriorityFour(t *testing.T) {
	r := validCreate()
	r.Priority = 4
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for P4", err)
	}
}

func TestCreateIssueRequest_Validate_TooManyLabels(t *testing.T) {
	r := validCreate()
	r.Labels = make([]string, MaxLabels+1)
	assertFieldError(t, r.Validate(), "labels", "too many")
}

func TestCreateIssueRequest_Validate_TooManyDependencies(t *testing.T) {
	r := validCreate()
	r.Dependencies = make([]string, MaxDependencies+1)
	assertFieldError(t, r.Validate(), "dependencies", "too many")
}

func TestCreateIssueRequest_Validate_NegativeEstimatedMinutes(t *testing.T) {
	r := validCreate()
	r.EstimatedMinutes = intPtr(-1)
	assertFieldError(t, r.Validate(), "estimated_minutes", "cannot be negative")
}

func TestCreateIssueRequest_Validate_ZeroEstimatedMinutes(t *testing.T) {
	r := validCreate()
	r.EstimatedMinutes = intPtr(0)
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for 0 estimated minutes", err)
	}
}

func TestCreateIssueRequest_Validate_InvalidDueAt(t *testing.T) {
	r := validCreate()
	r.DueAt = "not-a-date"
	assertFieldError(t, r.Validate(), "due_at", "must be a valid RFC 3339 timestamp")
}

func TestCreateIssueRequest_Validate_ValidDueAt(t *testing.T) {
	r := validCreate()
	r.DueAt = "2024-01-15T10:30:00Z"
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestCreateIssueRequest_Validate_InvalidDeferUntil(t *testing.T) {
	r := validCreate()
	r.DeferUntil = "tomorrow"
	assertFieldError(t, r.Validate(), "defer_until", "must be a valid RFC 3339 timestamp")
}

func TestCreateIssueRequest_Validate_MultipleErrors(t *testing.T) {
	r := CreateIssueRequest{
		Title:     "",
		IssueType: "",
		Priority:  10,
	}
	err := r.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want error")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatal("error is not *ValidationError")
	}
	if len(ve.Errors) != 3 {
		t.Errorf("len(Errors) = %d, want 3 (title, issue_type, priority)", len(ve.Errors))
	}
}

func TestCreateIssueRequest_Validate_ErrorsAs(t *testing.T) {
	r := validCreate()
	r.Title = ""
	err := r.Validate()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatal("errors.As failed")
	}
	if len(ve.Errors) == 0 {
		t.Error("extracted ValidationError has no errors")
	}
}

func TestCreateIssueRequest_Validate_NilReceiver(t *testing.T) {
	var r *CreateIssueRequest
	err := r.Validate()
	assertFieldError(t, err, "request", "is nil")
}

// --- PatchIssueRequest tests ---

func TestPatchIssueRequest_Validate_Empty(t *testing.T) {
	r := PatchIssueRequest{}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for no-op update", err)
	}
}

func TestPatchIssueRequest_Validate_ValidTitle(t *testing.T) {
	r := PatchIssueRequest{Title: strPtr("new title")}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestPatchIssueRequest_Validate_EmptyTitle(t *testing.T) {
	r := PatchIssueRequest{Title: strPtr("")}
	assertFieldError(t, r.Validate(), "title", "cannot be empty")
}

func TestPatchIssueRequest_Validate_WhitespaceTitle(t *testing.T) {
	r := PatchIssueRequest{Title: strPtr("  ")}
	assertFieldError(t, r.Validate(), "title", "cannot be empty")
}

func TestPatchIssueRequest_Validate_TitleTooLong(t *testing.T) {
	r := PatchIssueRequest{Title: strPtr(strings.Repeat("x", MaxTitleLength+1))}
	assertFieldError(t, r.Validate(), "title", "must be 500 characters or less")
}

func TestPatchIssueRequest_Validate_ValidStatus(t *testing.T) {
	r := PatchIssueRequest{Status: strPtr("in_progress")}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestPatchIssueRequest_Validate_InvalidStatus(t *testing.T) {
	r := PatchIssueRequest{Status: strPtr("banana")}
	assertFieldError(t, r.Validate(), "status", "must be a valid status")
}

func TestPatchIssueRequest_Validate_EmptyStatus(t *testing.T) {
	r := PatchIssueRequest{Status: strPtr("")}
	assertFieldError(t, r.Validate(), "status", "must be a valid status")
}

func TestPatchIssueRequest_Validate_InternalStatusRejected(t *testing.T) {
	// Internal statuses (tombstone, pinned, hooked) must not be settable via API.
	for _, s := range []string{"tombstone", "pinned", "hooked"} {
		r := PatchIssueRequest{Status: strPtr(s)}
		assertFieldError(t, r.Validate(), "status", "must be a valid status")
	}
}

func TestPatchIssueRequest_Validate_ValidPriority(t *testing.T) {
	r := PatchIssueRequest{Priority: intPtr(2)}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestPatchIssueRequest_Validate_InvalidPriority(t *testing.T) {
	r := PatchIssueRequest{Priority: intPtr(5)}
	assertFieldError(t, r.Validate(), "priority", "must be between 0 and 4")
}

func TestPatchIssueRequest_Validate_ValidIssueType(t *testing.T) {
	r := PatchIssueRequest{IssueType: strPtr("bug")}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestPatchIssueRequest_Validate_InvalidIssueType(t *testing.T) {
	r := PatchIssueRequest{IssueType: strPtr("invalid")}
	assertFieldError(t, r.Validate(), "issue_type", "must be one of")
}

func TestPatchIssueRequest_Validate_EmptyIssueType(t *testing.T) {
	r := PatchIssueRequest{IssueType: strPtr("")}
	assertFieldError(t, r.Validate(), "issue_type", "must be one of")
}

func TestPatchIssueRequest_Validate_NegativeEstimatedMinutes(t *testing.T) {
	r := PatchIssueRequest{EstimatedMinutes: intPtr(-5)}
	assertFieldError(t, r.Validate(), "estimated_minutes", "cannot be negative")
}

func TestPatchIssueRequest_Validate_InvalidDueAt(t *testing.T) {
	r := PatchIssueRequest{DueAt: strPtr("bad")}
	assertFieldError(t, r.Validate(), "due_at", "must be a valid RFC 3339 timestamp")
}

func TestPatchIssueRequest_Validate_InvalidDeferUntil(t *testing.T) {
	r := PatchIssueRequest{DeferUntil: strPtr("bad")}
	assertFieldError(t, r.Validate(), "defer_until", "must be a valid RFC 3339 timestamp")
}

func TestPatchIssueRequest_Validate_LabelExclusivity(t *testing.T) {
	r := PatchIssueRequest{
		SetLabels: []string{"a"},
		AddLabels: []string{"b"},
	}
	assertFieldError(t, r.Validate(), "set_labels", "cannot be combined with add_labels or remove_labels")
}

func TestPatchIssueRequest_Validate_LabelExclusivityWithRemove(t *testing.T) {
	r := PatchIssueRequest{
		SetLabels:    []string{"a"},
		RemoveLabels: []string{"b"},
	}
	assertFieldError(t, r.Validate(), "set_labels", "cannot be combined with add_labels or remove_labels")
}

func TestPatchIssueRequest_Validate_SetLabelsOnly(t *testing.T) {
	r := PatchIssueRequest{SetLabels: []string{"a", "b"}}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestPatchIssueRequest_Validate_AddRemoveLabelsOnly(t *testing.T) {
	r := PatchIssueRequest{
		AddLabels:    []string{"a"},
		RemoveLabels: []string{"b"},
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestPatchIssueRequest_Validate_EmptySetLabelsNoConflict(t *testing.T) {
	// Empty SetLabels (clear all) does not conflict with AddLabels.
	r := PatchIssueRequest{
		SetLabels: []string{},
		AddLabels: []string{"x"},
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil (empty SetLabels = clear all, not conflict)", err)
	}
}

func TestPatchIssueRequest_Validate_SetLabelsWithEmptyAdd(t *testing.T) {
	// SetLabels with values + empty AddLabels = no conflict.
	r := PatchIssueRequest{
		SetLabels: []string{"a"},
		AddLabels: []string{},
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil (empty AddLabels treated as absent)", err)
	}
}

func TestPatchIssueRequest_Validate_TooManyAddLabels(t *testing.T) {
	r := PatchIssueRequest{AddLabels: make([]string, MaxLabels+1)}
	assertFieldError(t, r.Validate(), "add_labels", "too many")
}

func TestPatchIssueRequest_Validate_TooManySetLabels(t *testing.T) {
	r := PatchIssueRequest{SetLabels: make([]string, MaxLabels+1)}
	assertFieldError(t, r.Validate(), "set_labels", "too many")
}

func TestPatchIssueRequest_Validate_TooManyRemoveLabels(t *testing.T) {
	r := PatchIssueRequest{RemoveLabels: make([]string, MaxLabels+1)}
	assertFieldError(t, r.Validate(), "remove_labels", "too many")
}

func TestPatchIssueRequest_Validate_MultipleErrors(t *testing.T) {
	r := PatchIssueRequest{
		Status:   strPtr("banana"),
		Priority: intPtr(99),
	}
	err := r.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want error")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatal("error is not *ValidationError")
	}
	if len(ve.Errors) != 2 {
		t.Errorf("len(Errors) = %d, want 2 (status, priority)", len(ve.Errors))
	}
}

func TestPatchIssueRequest_Validate_NilReceiver(t *testing.T) {
	var r *PatchIssueRequest
	err := r.Validate()
	assertFieldError(t, err, "request", "is nil")
}

// --- helpers ---

// assertFieldError checks that err is a *ValidationError containing at least
// one FieldError with the given field whose message contains msgSubstr.
func assertFieldError(t *testing.T, err error, field, msgSubstr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Validate() = nil, want error on field %q", field)
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error %T is not *ValidationError", err)
	}
	for _, fe := range ve.Errors {
		if fe.Field == field && strings.Contains(fe.Message, msgSubstr) {
			return
		}
	}
	t.Errorf("no FieldError{Field: %q, Message containing %q} in %v", field, msgSubstr, ve.Errors)
}
