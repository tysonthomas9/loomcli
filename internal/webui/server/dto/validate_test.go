package dto

import (
	"errors"
	"testing"
)

// Compile-time check: ValidationError implements error.
var _ error = (*ValidationError)(nil)

func TestValidationError_ErrorSingle(t *testing.T) {
	ve := &ValidationError{Errors: []FieldError{{Field: "title", Message: "is required"}}}
	want := "validation failed: title: is required"
	if got := ve.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestValidationError_ErrorMultiple(t *testing.T) {
	ve := &ValidationError{Errors: []FieldError{
		{Field: "title", Message: "is required"},
		{Field: "priority", Message: "must be between 0 and 4 (got 5)"},
		{Field: "issue_type", Message: "is required"},
	}}
	want := "validation failed: 3 field errors"
	if got := ve.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestValidationError_FieldMap(t *testing.T) {
	ve := &ValidationError{Errors: []FieldError{
		{Field: "title", Message: "is required"},
		{Field: "priority", Message: "must be between 0 and 4 (got 5)"},
	}}
	m := ve.FieldMap()
	if len(m) != 2 {
		t.Fatalf("FieldMap() len = %d, want 2", len(m))
	}
	if m["title"] != "is required" {
		t.Errorf("FieldMap()[title] = %v, want %q", m["title"], "is required")
	}
	if m["priority"] != "must be between 0 and 4 (got 5)" {
		t.Errorf("FieldMap()[priority] = %v, want %q", m["priority"], "must be between 0 and 4 (got 5)")
	}
}

func TestValidationError_FieldMapDuplicateField(t *testing.T) {
	ve := &ValidationError{Errors: []FieldError{
		{Field: "title", Message: "is required"},
		{Field: "title", Message: "must be 500 characters or less"},
	}}
	m := ve.FieldMap()
	if len(m) != 1 {
		t.Fatalf("FieldMap() len = %d, want 1 (duplicate field)", len(m))
	}
	if m["title"] != "is required" {
		t.Errorf("FieldMap()[title] = %v, want first error %q", m["title"], "is required")
	}
}

func TestValidationError_ErrorsAs(t *testing.T) {
	var err error = &ValidationError{Errors: []FieldError{{Field: "x", Message: "y"}}}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatal("errors.As failed to extract *ValidationError")
	}
	if len(ve.Errors) != 1 {
		t.Errorf("extracted ValidationError has %d errors, want 1", len(ve.Errors))
	}
}

func TestValidationBuilder_NoErrors(t *testing.T) {
	var b validationBuilder
	if err := b.build(); err != nil {
		t.Errorf("build() = %v, want nil", err)
	}
}

func TestValidationBuilder_WithErrors(t *testing.T) {
	var b validationBuilder
	b.add("field1", "msg1")
	b.add("field2", "msg2")
	err := b.build()
	if err == nil {
		t.Fatal("build() = nil, want error")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatal("build() did not return *ValidationError")
	}
	if len(ve.Errors) != 2 {
		t.Errorf("len(Errors) = %d, want 2", len(ve.Errors))
	}
}
