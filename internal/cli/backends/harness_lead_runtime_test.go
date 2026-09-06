package backends

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

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
	// Both pinned explicitly: agent shells export LOOM_AGENT_MODEL, so an
	// argv assertion that trusts the ambient environment is red for whoever
	// runs the suite from inside the fleet and green for everyone else.
	t.Setenv("LOOM_AGENT_MODEL", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
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
	if len(captured.Args) != 3 || captured.Args[0] != "--session-id" || captured.Args[2] != "--dangerously-skip-permissions" {
		t.Fatalf("captured args = %#v, want [--session-id <uuid> --dangerously-skip-permissions]", captured.Args)
	}
	if captured.HarnessSessionID == "" || captured.HarnessSessionID != captured.Args[1] {
		t.Fatalf("HarnessSessionID = %q, want the --session-id value %q", captured.HarnessSessionID, captured.Args[1])
	}
	if uuid.Validate(captured.HarnessSessionID) != nil {
		t.Fatalf("HarnessSessionID = %q, want a valid UUID", captured.HarnessSessionID)
	}
	if captured.WorkDir != "/repo" || captured.Prompt != "prompt" {
		t.Fatalf("captured workdir/prompt = %q/%q", captured.WorkDir, captured.Prompt)
	}
	var foundWorktree, foundClaudeVirtualScroll bool
	for _, kv := range captured.Env {
		if kv == "LOOM_WORKTREE_PATH=/repo" {
			foundWorktree = true
		}
		if kv == claudeVirtualScrollEnv {
			foundClaudeVirtualScroll = true
		}
	}
	if !foundWorktree {
		t.Fatalf("captured env missing LOOM_WORKTREE_PATH: %#v", captured.Env)
	}
	if !foundClaudeVirtualScroll {
		t.Fatalf("captured env missing Claude virtual scroll mode: %#v", captured.Env)
	}
}

// The lead's controlled launch pins the model from the profile's provisioned
// baseline, so an in-session /model save cannot change what the NEXT lead
// session boots as.
func TestRunControlledLeadRuntimeClaudePinsProvisionedModel(t *testing.T) {
	t.Setenv("LOOM_AGENT_MODEL", "")
	t.Setenv("CLAUDE_CONFIG_DIR", writePinnedProfile(t, "settings.json", `{"model":"opus[1m]"}`))
	captured := installFakeHarnessLead(t)

	handled, err := RunControlledLeadRuntime(context.Background(), nil, "WS", "nova", "lead-session", "/repo", "prompt", "claude")
	if err != nil || !handled {
		t.Fatalf("RunControlledLeadRuntime() = %v/%v", handled, err)
	}
	want := []string{"--session-id", captured.HarnessSessionID, "--dangerously-skip-permissions", "--model", "opus[1m]"}
	if !reflect.DeepEqual(captured.Args, want) {
		t.Fatalf("captured args = %#v, want %#v", captured.Args, want)
	}
}

func TestRunControlledLeadRuntimeDispatchesGenericBackends(t *testing.T) {
	cases := map[string]struct {
		args   []string
		binary string // backend name and exec binary differ for cursor (cursor-agent)
	}{
		"gemini":   {[]string{"--approval-mode=yolo"}, "gemini"},
		"cursor":   {[]string{"--force"}, "cursor-agent"},
		"opencode": {nil, "opencode"},
	}
	// Same reason as the claude case above: opencode's interactive args carry
	// the role model, so an agent shell's LOOM_AGENT_MODEL makes this argv
	// assertion depend on who runs the suite.
	t.Setenv("LOOM_AGENT_MODEL", "")
	for backend, want := range cases {
		captured := installFakeHarnessLead(t)
		handled, err := RunControlledLeadRuntime(context.Background(), nil, "WS", "nova", "lead-session", "/repo", "prompt", backend)
		if err != nil {
			t.Fatalf("%s: RunControlledLeadRuntime() error = %v", backend, err)
		}
		if !handled {
			t.Fatalf("%s lead should be handled by the controlled runtime", backend)
		}
		if captured.Backend != backend || captured.BinaryPath != want.binary {
			t.Fatalf("%s: captured config = %+v", backend, captured)
		}
		if !slices.Equal(captured.Args, want.args) {
			t.Fatalf("%s: captured args = %q, want %q", backend, captured.Args, want.args)
		}
		if backend == "opencode" && captured.PromptFlag != "--prompt" {
			t.Fatalf("opencode: PromptFlag = %q, want --prompt", captured.PromptFlag)
		}
		for _, kv := range captured.Env {
			if strings.HasPrefix(kv, "CLAUDE_CODE_NO_FLICKER=") {
				t.Fatalf("%s: Claude-only virtual scroll mode leaked into env: %#v", backend, captured.Env)
			}
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
