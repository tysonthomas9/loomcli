package workspaceid

import "testing"

func TestShort(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "empty", id: "", want: "default"},
		{name: "uuid", id: "a1b2c3d4-e5f6-7890-abcd-ef1234567890", want: "a1b2c3d4"},
		{name: "short", id: "abc", want: "abc"},
		{name: "exact", id: "abcdefgh", want: "abcdefgh"},
		{name: "long", id: "abcdefghi", want: "abcdefgh"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Short(tt.id); got != tt.want {
				t.Fatalf("Short(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		envVal string
		want   string
	}{
		{name: "explicit", id: "ws-12345", want: "ws-12345"},
		{name: "explicit ignores env", id: "ws-12345", envVal: "env-67890", want: "ws-12345"},
		{name: "environment", envVal: "env-67890", want: "env-67890"},
		{name: "absent", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOOM_WORKSPACE_ID", tt.envVal)
			if got := Resolve(tt.id); got != tt.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}
