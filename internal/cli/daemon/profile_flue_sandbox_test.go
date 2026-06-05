package daemon

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestApplyProfileField_FlueSandbox(t *testing.T) {
	p := &domain.DaemonProfile{}

	if err := applyProfileField(p, "flue_sandbox", "daytona", false); err != nil {
		t.Fatalf("set daytona: %v", err)
	}
	if p.FlueSandbox != "daytona" {
		t.Errorf("FlueSandbox = %q, want daytona", p.FlueSandbox)
	}

	// Case-insensitive + trimmed.
	if err := applyProfileField(p, "flue_sandbox", "  Local ", false); err != nil {
		t.Fatalf("set local: %v", err)
	}
	if p.FlueSandbox != "local" {
		t.Errorf("FlueSandbox = %q, want local", p.FlueSandbox)
	}

	// Invalid value rejected.
	if err := applyProfileField(p, "flue_sandbox", "vm", false); err == nil {
		t.Error("expected an error for an invalid flue_sandbox value")
	}

	// Unset reverts to empty (= default local).
	if err := applyProfileField(p, "flue_sandbox", "", true); err != nil {
		t.Fatalf("unset: %v", err)
	}
	if p.FlueSandbox != "" {
		t.Errorf("FlueSandbox after unset = %q, want empty", p.FlueSandbox)
	}
}
