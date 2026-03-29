package cli

import (
	"testing"
)

// ---------------------------------------------------------------------------
// isUUIDFormat tests (loomcli-n28bt.10)
// ---------------------------------------------------------------------------

func TestIsUUIDFormat(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid UUID v4",
			input: "550e8400-e29b-41d4-a716-446655440000",
			want:  true,
		},
		{
			name:  "valid UUID nil",
			input: "00000000-0000-0000-0000-000000000000",
			want:  true,
		},
		{
			name:  "workspace name",
			input: "my-workspace",
			want:  false,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
		{
			name:  "partial UUID",
			input: "550e8400-e29b-41d4",
			want:  false,
		},
		{
			name:  "UUID without dashes",
			input: "550e8400e29b41d4a716446655440000",
			want:  true, // uuid.Parse accepts this form
		},
		{
			name:  "just numbers",
			input: "12345",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUUIDFormat(tt.input)
			if got != tt.want {
				t.Errorf("isUUIDFormat(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveWorkerWorkspace tests (loomcli-n28bt.10)
// ---------------------------------------------------------------------------

func TestResolveWorkerWorkspace_AlreadyUUID(t *testing.T) {
	// When workspace is already a valid UUID, it should be returned as-is
	// without attempting to load config.
	uuid := "550e8400-e29b-41d4-a716-446655440000"
	got := resolveWorkerWorkspace(uuid)
	if got != uuid {
		t.Errorf("resolveWorkerWorkspace(%q) = %q, want %q", uuid, got, uuid)
	}
}

func TestResolveWorkerWorkspace_NonUUIDFallsBack(t *testing.T) {
	// When workspace is not a UUID and config is unavailable, the original
	// value should be returned as-is (graceful fallback).
	name := "my-workspace"
	got := resolveWorkerWorkspace(name)
	// Without a valid config file, it should fall back to returning the name.
	// We can't easily assert what it returns beyond "no panic", but since
	// LoadConfig will fail in a test env without config, we expect the name back.
	if got == "" {
		t.Error("resolveWorkerWorkspace returned empty string, want non-empty fallback")
	}
}
