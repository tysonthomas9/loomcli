//go:build !unix

package localworkspace

import (
	"strings"
	"testing"
)

func TestCredentialedGitFailsClosedWithoutProcessTreeIsolation(t *testing.T) {
	err := requireGitCredentialProcessIsolation()
	if err == nil || !strings.Contains(err.Error(), "process-tree isolation") {
		t.Fatalf("isolation error = %v, want explicit fail-closed result", err)
	}
}
