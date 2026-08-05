//go:build !unix

package localgit

import (
	"strings"
	"testing"
)

func TestCredentialedGitFailsClosedWithoutProcessTreeIsolation(t *testing.T) {
	err := requireCredentialProcessIsolation()
	if err == nil || !strings.Contains(err.Error(), "process-tree isolation") {
		t.Fatalf("requireCredentialProcessIsolation() error = %v", err)
	}
}
