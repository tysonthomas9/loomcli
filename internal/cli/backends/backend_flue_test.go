package backends

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// TestFlueBackendDispatch verifies the FlueBackend methods dispatch to the
// mockable invoker seams (parity with the other backends' dispatch tests).
func TestFlueBackendDispatch(t *testing.T) {
	f := &FlueBackend{}

	origNon := flueNonInteractiveInvoker
	t.Cleanup(func() { flueNonInteractiveInvoker = origNon })
	var nonWD, nonPrompt, nonAgent string
	flueNonInteractiveInvoker = func(workDir, prompt, agentName string, _ <-chan struct{}, _ *usage.Collector) error {
		nonWD, nonPrompt, nonAgent = workDir, prompt, agentName
		return nil
	}
	if err := f.InvokeNonInteractive("/w", "p", "ag", nil, nil); err != nil {
		t.Fatalf("InvokeNonInteractive: %v", err)
	}
	if nonWD != "/w" || nonPrompt != "p" || nonAgent != "ag" {
		t.Fatalf("non-interactive dispatch args = %q/%q/%q", nonWD, nonPrompt, nonAgent)
	}

	origInt := flueInteractiveInvoker
	t.Cleanup(func() { flueInteractiveInvoker = origInt })
	intCalled := false
	flueInteractiveInvoker = func(_, _, _ string) error { intCalled = true; return nil }
	if err := f.InvokeInteractive("/w", "p", "ag"); err != nil || !intCalled {
		t.Fatalf("InvokeInteractive dispatch: err=%v called=%v", err, intCalled)
	}

	origLead := flueInvokeLead
	t.Cleanup(func() { flueInvokeLead = origLead })
	leadCalled := false
	flueInvokeLead = func(_, _ string) error { leadCalled = true; return nil }
	if err := f.InvokeLead("/w", "p"); err != nil || !leadCalled {
		t.Fatalf("InvokeLead dispatch: err=%v called=%v", err, leadCalled)
	}
}

// Compile-time guarantee that FlueBackend keeps satisfying LeadServerBackend
// so `loom lead` continues to route to the server path.
var _ LeadServerBackend = (*FlueBackend)(nil)

func TestLeadServerBackendRouting(t *testing.T) {
	b, ok := cli.GetBackendByName(NameFlue)
	if !ok {
		t.Fatal("flue backend not registered")
	}
	if _, isLead := b.(LeadServerBackend); !isLead {
		t.Fatal("flue must implement LeadServerBackend (lead would fall back to one-shot interactive)")
	}
	// The one-shot/CLI-TUI backends must NOT be lead-server backends — `loom
	// lead` must keep using their interactive subprocess path, not a server.
	for _, name := range []string{"claude", "codex", "opencode", "gemini", "cursor"} {
		cb, ok := cli.GetBackendByName(name)
		if !ok {
			continue
		}
		if _, isLead := cb.(LeadServerBackend); isLead {
			t.Errorf("%s unexpectedly implements LeadServerBackend", name)
		}
	}
}

func TestParseCodexConfigModel(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "typical config with reasoning effort after model",
			in:   "model = \"gpt-5.5\"\nmodel_reasoning_effort = \"xhigh\"\n",
			want: "gpt-5.5",
		},
		{
			name: "must not match model_reasoning_effort",
			in:   "model_reasoning_effort = \"xhigh\"\npersonality = \"pragmatic\"\n",
			want: "",
		},
		{
			name: "ignores comments and stops at section",
			in:   "# a comment\nmodel = \"o3\"\n[projects.\"/x\"]\nmodel = \"ignored\"\n",
			want: "o3",
		},
		{
			name: "single quotes",
			in:   "model = 'gpt-5.5'\n",
			want: "gpt-5.5",
		},
		{
			name: "no model key",
			in:   "personality = \"pragmatic\"\n",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseCodexConfigModel(c.in); got != c.want {
				t.Errorf("parseCodexConfigModel = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveFlueModelPrecedence(t *testing.T) {
	// Explicit env wins over everything.
	t.Setenv(envFlueModel, "openrouter/some-model")
	if got := resolveFlueModel(); got != "openrouter/some-model" {
		t.Fatalf("LOOM_FLUE_MODEL not honored: got %q", got)
	}

	// Without LOOM_FLUE_MODEL, an Anthropic key selects the anthropic default
	// (and short-circuits before any codex-auth probing).
	t.Setenv(envFlueModel, "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	if got := resolveFlueModel(); got != defaultFlueModel {
		t.Fatalf("ANTHROPIC default not selected: got %q", got)
	}
}

func TestFlueRunArgs(t *testing.T) {
	args := flueRunArgs("agent", "/proj", `{"prompt":"x"}`)
	want := []string{"run", "agent", "--target", "node", "--root", "/proj", "--payload", `{"prompt":"x"}`}
	if len(args) != len(want) {
		t.Fatalf("arg count = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("arg[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}
