package agent

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLeadEnvResolversAndExecShellFallback(t *testing.T) {
	t.Setenv(envOrchestratorSessionID, " existing-session ")
	if got := resolveLeadOrchestratorSessionID(); got != "existing-session" {
		t.Fatalf("resolveLeadOrchestratorSessionID = %q", got)
	}
	t.Setenv(envOrchestratorSessionID, "")
	if got := resolveLeadOrchestratorSessionID(); !strings.HasPrefix(got, "lead-") {
		t.Fatalf("generated lead session id = %q", got)
	}

	t.Setenv(envAgentName, " nova ")
	if got := resolveLeadAgentID(); got != "nova" {
		t.Fatalf("resolveLeadAgentID = %q", got)
	}
	t.Setenv(envAgentName, "")
	if got := resolveLeadAgentID(); got != "lead" {
		t.Fatalf("default lead agent id = %q", got)
	}

	t.Setenv("USER", "")
	if got := leadSessionActor(); got != "unknown" {
		t.Fatalf("leadSessionActor without USER = %q", got)
	}

	t.Setenv("SHELL", filepath.Join(t.TempDir(), "missing-shell"))
	execShell(t.TempDir())
}
