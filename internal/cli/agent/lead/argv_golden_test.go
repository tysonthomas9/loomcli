package lead

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/agent"
)

// updateArgvGolden regenerates internal/cli/agent/lead/testdata/argv_golden/*.txt.
//
//	go test ./internal/cli/agent/lead -run TestLeadPromptArgvGolden -update-argv-golden
var updateArgvGolden = flag.Bool("update-argv-golden", false, "rewrite the lead prompt argv goldens")

const (
	goldenAssignment = "Epic EPIC-1 is yours. Backend: claude."
	goldenMessage    = "list open epics"
)

// legacyComposeLeadPrompt is composeLeadPrompt exactly as it read before the
// builtin:none work: string concatenation onto the base. It is kept as the
// regression floor - for every NON-EMPTY base the two must agree byte for byte.
// It is deliberately not used in production code.
func legacyComposeLeadPrompt(base, assignment, message string) string {
	if assignment != "" {
		base += "\n\n## Loom Backend Assignment\n\n" + assignment
	}
	if message != "" {
		base += "\n\n## User's Initial Request\n\n" + message +
			"\n\nAddress this request using the lead mode conventions above."
	}
	return base
}

// leadPromptBranch is one way `loom lead` resolves its base argv prompt.
type leadPromptBranch struct {
	name string
	base func(t *testing.T) string
}

// isolateLeadPromptEnv pins every environment input the prompt renderers read,
// so a golden cannot shift under the agent's own LOOM_* variables.
func isolateLeadPromptEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LOOM_AGENT_NAME", "")
	t.Setenv("LOOM_AGENT_ROLE", "")
	t.Setenv("LOOM_READ_ONLY", "")
	t.Setenv("LOOM_WORKSPACE", "")
}

func mustGenerateTerminalPrompt(t *testing.T, promptFile string) string {
	t.Helper()
	prompt, err := agent.GenerateTerminalPrompt(promptFile)
	if err != nil {
		t.Fatalf("GenerateTerminalPrompt(%q): %v", promptFile, err)
	}
	return prompt
}

func leadPromptBranches() []leadPromptBranch {
	return []leadPromptBranch{
		{
			name: "default-dedicated",
			base: func(*testing.T) string { return agent.LeadSafetyPrompt() },
		},
		{
			name: "default-non-dedicated",
			base: func(t *testing.T) string { return mustGenerateTerminalPrompt(t, "") },
		},
		{
			name: "prompt-file",
			base: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "role.md")
				if err := os.WriteFile(path, []byte("# Custom role\n\nDo the custom thing.\n"), 0o600); err != nil {
					t.Fatalf("write prompt file: %v", err)
				}
				return mustGenerateTerminalPrompt(t, path)
			},
		},
		{
			name: "inline-role-prompt",
			base: func(t *testing.T) string {
				prompt, err := agent.GenerateTerminalPromptText("# Inline role\n\nDo the inline thing.")
				if err != nil {
					t.Fatalf("GenerateTerminalPromptText: %v", err)
				}
				return prompt
			},
		},
		{
			name: "builtin-lead",
			base: func(t *testing.T) string { return mustGenerateTerminalPrompt(t, "builtin:lead") },
		},
		{
			name: "builtin-lead-profile",
			base: func(t *testing.T) string { return mustGenerateTerminalPrompt(t, "builtin:lead-profile") },
		},
		{
			name: "builtin-pr-review",
			base: func(t *testing.T) string { return mustGenerateTerminalPrompt(t, "builtin:pr-review") },
		},
	}
}

// goldenVariants renders one branch under the four per-session combinations
// into a single self-describing document, so one file pins one branch.
func goldenVariants(base string) string {
	var sb strings.Builder
	for _, v := range []struct {
		name       string
		assignment string
		message    string
	}{
		{"plain", "", ""},
		{"message", "", goldenMessage},
		{"assignment", goldenAssignment, ""},
		{"assignment+message", goldenAssignment, goldenMessage},
	} {
		sb.WriteString("=== VARIANT " + v.name + " ===\n")
		sb.WriteString(composeLeadPrompt(base, v.assignment, v.message))
		sb.WriteString("\n=== END " + v.name + " ===\n")
	}
	return sb.String()
}

// TestLeadPromptArgvGolden is the regression floor for applyLeadPromptContext:
// every pre-existing prompt branch must keep producing the exact bytes recorded
// under testdata/argv_golden. Any diff is a bug in composeLeadPrompt.
func TestLeadPromptArgvGolden(t *testing.T) {
	isolateLeadPromptEnv(t)

	for _, branch := range leadPromptBranches() {
		t.Run(branch.name, func(t *testing.T) {
			got := goldenVariants(branch.base(t))
			path := filepath.Join("testdata", "argv_golden", branch.name+".txt")
			if *updateArgvGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatalf("mkdir golden dir: %v", err)
				}
				if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(path) //nolint:gosec // G304: fixed testdata path
			if err != nil {
				t.Fatalf("read golden (regenerate with -update-argv-golden): %v", err)
			}
			if got != string(want) {
				t.Errorf("prompt bytes changed for branch %s\n--- got ---\n%s\n--- want ---\n%s", branch.name, got, want)
			}
		})
	}
}

// TestComposeLeadPromptMatchesLegacyForNonEmptyBase proves the []string+Join
// rewrite is a no-op for every base that could exist before builtin:none.
func TestComposeLeadPromptMatchesLegacyForNonEmptyBase(t *testing.T) {
	isolateLeadPromptEnv(t)

	for _, branch := range leadPromptBranches() {
		t.Run(branch.name, func(t *testing.T) {
			base := branch.base(t)
			if base == "" {
				t.Fatalf("branch %s produced an empty base prompt", branch.name)
			}
			for _, assignment := range []string{"", goldenAssignment} {
				for _, message := range []string{"", goldenMessage} {
					got := composeLeadPrompt(base, assignment, message)
					want := legacyComposeLeadPrompt(base, assignment, message)
					if got != want {
						t.Errorf("composeLeadPrompt diverged from legacy (assignment=%q message=%q)", assignment, message)
					}
				}
			}
		})
	}
}
