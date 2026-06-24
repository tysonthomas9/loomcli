package backends

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
)

func installFakeHarnessLead(t *testing.T) *leadcontrol.HarnessLeadRuntimeConfig {
	t.Helper()
	var captured leadcontrol.HarnessLeadRuntimeConfig
	orig := runHarnessLead
	runHarnessLead = func(_ context.Context, cfg leadcontrol.HarnessLeadRuntimeConfig) error {
		captured = cfg
		return nil
	}
	t.Cleanup(func() { runHarnessLead = orig })
	return &captured
}

func TestRunControlledLeadRuntimeDispatchesClaude(t *testing.T) {
	captured := installFakeHarnessLead(t)

	handled, err := RunControlledLeadRuntime(context.Background(), nil, "WS", "nova", "lead-session", "/repo", "prompt", "claude")
	if err != nil {
		t.Fatalf("RunControlledLeadRuntime() error = %v", err)
	}
	if !handled {
		t.Fatalf("claude lead should be handled by the controlled runtime")
	}
	if captured.Backend != "claude" || captured.BinaryPath != "claude" {
		t.Fatalf("captured config = %+v, want claude backend/binary", captured)
	}
	if len(captured.Args) != 1 || captured.Args[0] != "--dangerously-skip-permissions" {
		t.Fatalf("captured args = %#v", captured.Args)
	}
	if captured.WorkDir != "/repo" || captured.Prompt != "prompt" {
		t.Fatalf("captured workdir/prompt = %q/%q", captured.WorkDir, captured.Prompt)
	}
	var found bool
	for _, kv := range captured.Env {
		if kv == "LOOM_WORKTREE_PATH=/repo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("captured env missing LOOM_WORKTREE_PATH: %#v", captured.Env)
	}
}

func TestRunControlledLeadRuntimeDispatchesGenericBackends(t *testing.T) {
	cases := map[string][]string{
		"gemini":   {"--approval-mode=yolo"},
		"cursor":   {"--force"},
		"opencode": {"run", "--dir", "/repo", "--dangerously-skip-permissions"},
	}
	for backend, wantPrefix := range cases {
		captured := installFakeHarnessLead(t)
		handled, err := RunControlledLeadRuntime(context.Background(), nil, "WS", "nova", "lead-session", "/repo", "prompt", backend)
		if err != nil {
			t.Fatalf("%s: RunControlledLeadRuntime() error = %v", backend, err)
		}
		if !handled {
			t.Fatalf("%s lead should be handled by the controlled runtime", backend)
		}
		if captured.Backend != backend || captured.BinaryPath != backend {
			t.Fatalf("%s: captured config = %+v", backend, captured)
		}
		got := strings.Join(captured.Args, " ")
		if !strings.HasPrefix(got, strings.Join(wantPrefix, " ")) {
			t.Fatalf("%s: captured args = %q, want prefix %q", backend, got, strings.Join(wantPrefix, " "))
		}
	}
}

func TestRunControlledLeadRuntimeUnknownBackendNotHandled(t *testing.T) {
	installFakeHarnessLead(t)
	handled, err := RunControlledLeadRuntime(context.Background(), nil, "WS", "nova", "lead-session", "/repo", "prompt", "my-external-plugin")
	if err != nil {
		t.Fatalf("RunControlledLeadRuntime() error = %v", err)
	}
	if handled {
		t.Fatalf("unknown backend should fall back to plain interactive launch")
	}
}

func TestRunControlledLeadRuntimeEnvEscapeHatch(t *testing.T) {
	t.Setenv(envLeadControlled, "0")
	installFakeHarnessLead(t)
	handled, err := RunControlledLeadRuntime(context.Background(), nil, "WS", "nova", "lead-session", "/repo", "prompt", "claude")
	if err != nil {
		t.Fatalf("RunControlledLeadRuntime() error = %v", err)
	}
	if handled {
		t.Fatalf("LOOM_LEAD_CONTROLLED=0 should disable the controlled runtime")
	}
}
