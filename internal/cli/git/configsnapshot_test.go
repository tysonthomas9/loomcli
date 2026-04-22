package git

// configsnapshot_test.go is a Phase 1 diagnostic helper for loomcli-nfuq9.
// It reads the raw contents of a git config file and surfaces whether
// core.bare=true has been written.
//
// Lives in a _test.go file so it is never compiled into production binaries.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// snapshotGitConfig reads configPath and returns a short hash, the raw body,
// and whether [core] bare = true appears in the file.
//
// If the file does not exist yet (e.g. early in sandbox setup), it returns
// hash="<missing>", body="", hasBare=false without failing the test.
func snapshotGitConfig(t *testing.T, configPath string) (hash, body string, hasBare bool) {
	t.Helper()
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: test helper, caller controls path
	if err != nil {
		if os.IsNotExist(err) {
			return "<missing>", "", false
		}
		t.Fatalf("read %s: %v", configPath, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12], string(data), hasCoreBareTrue(string(data))
}

// hasCoreBareTrue returns true when the parsed ini-style git config contains
// `bare = true` under the [core] section (case-insensitive keys/values).
// This is the signal we are hunting in loomcli-nfuq9.
func hasCoreBareTrue(cfg string) bool {
	inCore := false
	for _, raw := range strings.Split(cfg, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			// Treat "[core]" and subsectioned "[core "x"]" as in-section so a
			// bare = true written under any core-scoped header is caught.
			trimmed := strings.ToLower(line)
			inCore = trimmed == "[core]" || strings.HasPrefix(trimmed, "[core ")
			continue
		}
		if !inCore {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if strings.EqualFold(key, "bare") && strings.EqualFold(val, "true") {
			return true
		}
	}
	return false
}
