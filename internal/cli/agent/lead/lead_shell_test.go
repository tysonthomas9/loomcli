package lead

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

func TestRefuseSandboxShellGuardsOnOccupantEnv(t *testing.T) {
	t.Setenv(placement.OccupantTokenEnv, "")
	var out strings.Builder
	if refuseSandboxShell(&out) {
		t.Fatal("refused an interactive shell outside a placement")
	}
	if out.Len() != 0 {
		t.Fatalf("wrote %q outside a placement", out.String())
	}

	t.Setenv(placement.OccupantTokenEnv, "occupant-token")
	if !refuseSandboxShell(&out) {
		t.Fatal("did not refuse an interactive shell inside a placement")
	}
	if !strings.Contains(out.String(), "refusing to drop to an interactive shell") {
		t.Fatalf("refusal message = %q", out.String())
	}
}
