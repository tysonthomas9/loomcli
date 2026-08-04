package workspace

import "testing"

func TestShortWorkspaceID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{
			name: "empty string returns default",
			id:   "",
			want: "default",
		},
		{
			name: "full UUID returns first 8 chars",
			id:   "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
			want: "a1b2c3d4",
		},
		{
			name: "string shorter than 8 returns as-is",
			id:   "abc",
			want: "abc",
		},
		{
			name: "exactly 8 chars returns as-is",
			id:   "abcdefgh",
			want: "abcdefgh",
		},
		{
			name: "9 chars returns first 8",
			id:   "abcdefghi",
			want: "abcdefgh",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShortWorkspaceID(tt.id)
			if got != tt.want {
				t.Errorf("ShortWorkspaceID(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestResolveWorkspaceID(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		envVal string
		want   string
	}{
		{
			name: "non-empty ID returned as-is",
			id:   "ws-12345",
			want: "ws-12345",
		},
		{
			name:   "non-empty ID ignores env",
			id:     "ws-12345",
			envVal: "env-67890",
			want:   "ws-12345",
		},
		{
			name:   "empty ID with env returns env value",
			id:     "",
			envVal: "env-67890",
			want:   "env-67890",
		},
		{
			name:   "empty ID without env returns empty string",
			id:     "",
			envVal: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVal != "" {
				t.Setenv("LOOM_WORKSPACE_ID", tt.envVal)
			} else {
				t.Setenv("LOOM_WORKSPACE_ID", "")
			}

			got := ResolveWorkspaceID(tt.id)
			if got != tt.want {
				t.Errorf("ResolveWorkspaceID(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}
