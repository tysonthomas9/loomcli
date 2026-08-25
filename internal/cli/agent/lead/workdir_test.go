package lead

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// isolateLeadEnv clears every input to lead workdir resolution so a test
// declares its own precedence branch and never inherits the operator's shell.
func isolateLeadEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv(localworkspace.EnvLeadWorkdir, "")
}

func chdirTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir()) // macOS /var -> /private/var
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return dir
}

func TestResolveLeadWorkdirPrefersEnvOverride(t *testing.T) {
	isolateLeadEnv(t)
	cwd := chdirTemp(t)
	override := filepath.Join(t.TempDir(), "lead-elsewhere")
	t.Setenv(localworkspace.EnvLeadWorkdir, override)

	dir, dedicated, err := resolveLeadWorkdir(context.Background())
	if err != nil {
		t.Fatalf("resolveLeadWorkdir: %v", err)
	}
	if dir != override || !dedicated {
		t.Fatalf("resolveLeadWorkdir = (%q, %v), want (%q, true)", dir, dedicated, override)
	}
	if dir == cwd {
		t.Fatal("override must not resolve to the current directory")
	}
	if info, statErr := os.Stat(override); statErr != nil || !info.IsDir() {
		t.Fatalf("override directory not created: %v", statErr)
	}
}

func TestResolveLeadWorkdirUsesWorkspaceLeadDir(t *testing.T) {
	isolateLeadEnv(t)
	chdirTemp(t)
	wsPath := t.TempDir()
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.Workspaces["E2E"] = bootstrap.WorkspaceLocalState{Path: wsPath}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}
	t.Setenv("LOOM_WORKSPACE", "E2E")

	dir, dedicated, err := resolveLeadWorkdir(context.Background())
	if err != nil {
		t.Fatalf("resolveLeadWorkdir: %v", err)
	}
	want := filepath.Join(wsPath, localworkspace.LeadDirName)
	if dir != want || !dedicated {
		t.Fatalf("resolveLeadWorkdir = (%q, %v), want (%q, true)", dir, dedicated, want)
	}
	if info, statErr := os.Stat(want); statErr != nil || !info.IsDir() {
		t.Fatalf("lead directory not created: %v", statErr)
	}
}

func TestResolveLeadWorkdirFallsBackToGetwd(t *testing.T) {
	isolateLeadEnv(t)
	cwd := chdirTemp(t)

	dir, dedicated, err := resolveLeadWorkdir(context.Background())
	if err != nil {
		t.Fatalf("resolveLeadWorkdir: %v", err)
	}
	if dir != cwd {
		t.Fatalf("resolveLeadWorkdir dir = %q, want cwd %q", dir, cwd)
	}
	if dedicated {
		t.Fatal("os.Getwd fallback must not be reported as a dedicated lead directory")
	}
}

func TestSeedLeadWorkdirFilesWritesBoth(t *testing.T) {
	dir := t.TempDir()
	seedLeadWorkdirFiles(dir)

	agents := readSeeded(t, filepath.Join(dir, leadAgentsFileName))
	if !strings.Contains(agents, "INTERACTIVE MODE: Project Lead") {
		t.Fatalf("AGENTS.md missing the lead persona:\n%s", agents)
	}
	// The safety block is per-run and per-backend; a static copy would go stale.
	if strings.Contains(agents, "Multi-Agent Safety Rules") {
		t.Fatal("AGENTS.md must not contain the runtime safety guardrails")
	}
	claude := readSeeded(t, filepath.Join(dir, leadClaudeFileName))
	if !strings.Contains(claude, leadAgentsFileName) {
		t.Fatalf("CLAUDE.md = %q, want a pointer to AGENTS.md", claude)
	}
}

func TestSeedLeadWorkdirFilesNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	const edited = "operator's own lead persona\n"
	for _, name := range []string{leadAgentsFileName, leadClaudeFileName} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(edited), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	seedLeadWorkdirFiles(dir)

	for _, name := range []string{leadAgentsFileName, leadClaudeFileName} {
		if got := readSeeded(t, filepath.Join(dir, name)); got != edited {
			t.Fatalf("%s = %q, want the operator edit preserved byte-for-byte", name, got)
		}
	}
}

func readSeeded(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test-local temp path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestGenerateLeadTerminalPromptSeedAndShrinkPredicate walks every branch of
// the predicate: only the built-in prompt in a dedicated workdir shrinks argv.
func TestGenerateLeadTerminalPromptSeedAndShrinkPredicate(t *testing.T) {
	promptFileDir := t.TempDir()
	promptFile := filepath.Join(promptFileDir, "operator.md")
	if err := os.WriteFile(promptFile, []byte("Operator prompt"), 0o644); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}

	inlineRegistration := func(t *testing.T) leadSessionRegistration {
		t.Helper()
		st := memstore.New()
		if _, err := st.Roles().Create(context.Background(), store.RoleCreate{
			WorkspaceKey: "E2E",
			Name:         "lead",
			Kind:         string(domain.RoleKindInteractive),
			Prompt:       "Inline role prompt",
		}); err != nil {
			t.Fatalf("create role: %v", err)
		}
		return leadSessionRegistration{handle: &bootstrap.StoreHandle{Store: st}, Workspace: "E2E"}
	}

	tests := []struct {
		name       string
		dedicated  bool
		promptFile string
		inline     bool
		wantSeed   bool
		wantPrompt func() string
	}{
		{
			name:       "dedicated builtin shrinks to the safety block",
			dedicated:  true,
			wantSeed:   true,
			wantPrompt: agent.LeadSafetyPrompt,
		},
		{
			name:       "fallback builtin keeps the full lead prompt",
			dedicated:  false,
			wantSeed:   false,
			wantPrompt: agent.GenerateLeadPrompt,
		},
		{
			name:       "prompt file wins and clears the predicate",
			dedicated:  true,
			promptFile: promptFile,
			wantSeed:   false,
			wantPrompt: func() string { return "Operator prompt\n\n" + agent.LeadSafetyPrompt() },
		},
		{
			name:       "inline role prompt wins and clears the predicate",
			dedicated:  true,
			inline:     true,
			wantSeed:   false,
			wantPrompt: func() string { return "Inline role prompt\n\n" + agent.LeadSafetyPrompt() },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateLeadEnv(t)
			old := leadPromptFile
			leadPromptFile = tc.promptFile
			t.Cleanup(func() { leadPromptFile = old })

			registration := leadSessionRegistration{}
			if tc.inline {
				registration = inlineRegistration(t)
			}

			prompt, seedAndShrink, err := generateLeadTerminalPrompt(context.Background(), registration, tc.dedicated)
			if err != nil {
				t.Fatalf("generateLeadTerminalPrompt: %v", err)
			}
			if seedAndShrink != tc.wantSeed {
				t.Fatalf("seedAndShrink = %v, want %v", seedAndShrink, tc.wantSeed)
			}
			if want := tc.wantPrompt(); prompt != want {
				t.Fatalf("prompt = %q, want %q", prompt, want)
			}
			if !strings.Contains(prompt, "Multi-Agent Safety Rules") {
				t.Fatal("argv prompt must always carry the runtime safety guardrails")
			}
		})
	}
}

// runLeadWithMock runs runLead against a recording backend with the controlled
// (harness-wrapper PTY) runtime disabled, and swallows the banner.
func runLeadWithMock(t *testing.T) *mockBackend {
	t.Helper()
	t.Setenv("LOOM_LEAD_CONTROLLED", "0")
	oldPromptFile, oldMessage := leadPromptFile, leadMessage
	leadPromptFile, leadMessage = "", ""
	t.Cleanup(func() { leadPromptFile, leadMessage = oldPromptFile, oldMessage })

	cli.TestingResetBackendState(t)
	mock := &mockBackend{name: "claude"}
	cli.RegisterBackend(mock)
	_ = cli.SetBackend("claude")

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	runLead(nil, nil)
	_ = w.Close()
	os.Stdout = oldStdout
	go func() { _, _ = io.Copy(io.Discard, r) }()

	if len(mock.interactiveCalls) != 1 {
		t.Fatalf("expected 1 backend invocation, got %d", len(mock.interactiveCalls))
	}
	return mock
}

// TestRunLeadDedicatedWorkdirSeedsAndShrinks covers the whole feature end to
// end: lead runs in its own directory, the persona is on disk as ambient
// instructions, and argv carries only the runtime safety block.
func TestRunLeadDedicatedWorkdirSeedsAndShrinks(t *testing.T) {
	isolateLeadEnv(t)
	cwd := chdirTemp(t)
	leadDir := filepath.Join(t.TempDir(), "lead")
	t.Setenv(localworkspace.EnvLeadWorkdir, leadDir)

	inv := runLeadWithMock(t).interactiveCalls[0]

	if inv.workDir != leadDir {
		t.Fatalf("workDir = %q, want dedicated lead dir %q", inv.workDir, leadDir)
	}
	if inv.workDir == cwd {
		t.Fatal("lead must not run in the operator's current directory")
	}
	if want := agent.LeadSafetyPrompt(); inv.prompt != want {
		t.Fatalf("argv prompt = %q, want the safety block alone %q", inv.prompt, want)
	}
	for _, name := range []string{leadAgentsFileName, leadClaudeFileName} {
		if _, err := os.Stat(filepath.Join(leadDir, name)); err != nil {
			t.Fatalf("%s not seeded in the lead workdir: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cwd, leadAgentsFileName)); !os.IsNotExist(err) {
		t.Fatalf("seeding leaked into the current directory: %v", err)
	}
}

// TestRunLeadFallbackIsInert pins the escape valve: outside a workspace lead
// still runs in os.Getwd, writes nothing there, and gets the FULL lead prompt
// on argv. The pre-existing AGENTS.md is the dangerous case - never-overwrite
// would otherwise leave another project's instructions as lead's own.
func TestRunLeadFallbackIsInert(t *testing.T) {
	isolateLeadEnv(t)
	cwd := chdirTemp(t)
	const foreign = "# Some other project\n\nDo unrelated things.\n"
	if err := os.WriteFile(filepath.Join(cwd, leadAgentsFileName), []byte(foreign), 0o644); err != nil {
		t.Fatalf("write foreign AGENTS.md: %v", err)
	}

	inv := runLeadWithMock(t).interactiveCalls[0]

	if inv.workDir != cwd {
		t.Fatalf("workDir = %q, want cwd %q", inv.workDir, cwd)
	}
	if want := agent.GenerateLeadPrompt(); inv.prompt != want {
		t.Fatalf("argv prompt = %q, want the full lead prompt", inv.prompt)
	}
	if got := readSeeded(t, filepath.Join(cwd, leadAgentsFileName)); got != foreign {
		t.Fatalf("foreign AGENTS.md = %q, want it untouched", got)
	}
	if _, err := os.Stat(filepath.Join(cwd, leadClaudeFileName)); !os.IsNotExist(err) {
		t.Fatalf("fallback must not seed CLAUDE.md: %v", err)
	}
}
