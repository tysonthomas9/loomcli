package sourcecontrolcoord

import "testing"

func TestValidateDiffPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"empty string", "", false},
		{"absolute unix", "/etc/passwd", false},
		{"dotdot only", "..", false},
		{"dotdot prefix", "../x", false},
		{"cleans to dotdot", "a/../../b", false},
		{"dot path", ".", false},
		{"valid nested file", "src/main.go", true},
		{"valid deep path", "a/b/c.txt", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validateDiffPath(tt.path); got != tt.want {
				t.Errorf("validateDiffPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
