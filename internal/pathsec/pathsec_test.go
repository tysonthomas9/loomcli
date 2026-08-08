package pathsec

import "testing"

func TestIsDeniedPath(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		denied bool
	}{
		{"allowed go file", "main.go", false},
		{"allowed markdown file", "README.md", false},
		{"allowed typescript file", "src/app.ts", false},
		{"denied pem extension", "secret.pem", true},
		{"denied key extension", "app.key", true},
		{"denied private key filename", "id_rsa", true},
		{"denied env filename", ".env", true},
		{"denied netrc filename", ".netrc", true},
		{"case insensitive extension", "SECRET.PEM", true},
		{"case insensitive filename", "ID_RSA", true},
		{"denied filename in subdir", "config/id_ed25519", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDeniedPath(tt.path); got != tt.denied {
				t.Errorf("IsDeniedPath(%q) = %v, want %v", tt.path, got, tt.denied)
			}
		})
	}
}

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
			if got := ValidateDiffPath(tt.path); got != tt.want {
				t.Errorf("ValidateDiffPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
