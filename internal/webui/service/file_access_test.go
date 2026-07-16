package service

import "testing"

func TestIsSensitiveFilePath(t *testing.T) {
	for _, path := range []string{
		".env", ".ENV.local", "config/.env.production", ".netrc", "home/.NETRC",
		"server.pem", "tls/client.P12", ".ssh/id_rsa", ".ssh/id_ed25519_work",
	} {
		if !IsSensitiveFilePath(path) {
			t.Errorf("IsSensitiveFilePath(%q) = false", path)
		}
	}
	for _, path := range []string{"README.md", "environment.go", "keys/id_rsa.pub", "certs/notes.txt"} {
		if IsSensitiveFilePath(path) {
			t.Errorf("IsSensitiveFilePath(%q) = true", path)
		}
	}
}
