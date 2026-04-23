package service

import "testing"

func TestValidateWorkspaceCreateRequest_TemplateTypeNotImplemented(t *testing.T) {
	req := &WorkspaceCreateRequest{Name: "test", Type: "template"}
	err := validateWorkspaceCreateRequest(req)
	if err == nil {
		t.Fatal("expected error for template type, got nil")
	}
	if err.Kind != KindNotImplemented {
		t.Errorf("Kind = %q, want %q", err.Kind, KindNotImplemented)
	}
	if err.Message != "template workspace type is not yet supported" {
		t.Errorf("Message = %q, want %q", err.Message, "template workspace type is not yet supported")
	}
}

func TestValidateWorkspaceCreateRequest_EmptyTypeStillValidationError(t *testing.T) {
	req := &WorkspaceCreateRequest{Name: "test", Type: ""}
	err := validateWorkspaceCreateRequest(req)
	if err == nil {
		t.Fatal("expected error for missing type, got nil")
	}
	if err.Kind != KindValidation {
		t.Errorf("Kind = %q, want %q", err.Kind, KindValidation)
	}
}

func TestValidateWorkspaceCreateRequest_InvalidTypeStillValidationError(t *testing.T) {
	req := &WorkspaceCreateRequest{Name: "test", Type: "bogus"}
	err := validateWorkspaceCreateRequest(req)
	if err == nil {
		t.Fatal("expected error for invalid type, got nil")
	}
	if err.Kind != KindValidation {
		t.Errorf("Kind = %q, want %q", err.Kind, KindValidation)
	}
}
