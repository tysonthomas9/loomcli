package backends

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// envValue searches a []string env slice for a key and returns the last
// matching value (exec.Cmd uses last-wins semantics for duplicate keys).
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	val := ""
	found := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			val = entry[len(prefix):]
			found = true
		}
	}
	return val, found
}

// envHasKey checks whether a key exists in a []string env slice.
func envHasKey(env []string, key string) bool {
	_, ok := envValue(env, key)
	return ok
}

type buildFunc func(workDir, prompt, agentName string) *exec.Cmd

func TestBuildInteractiveCmd_PromptInArgs(t *testing.T) {
	t.Setenv("LOOM_OPENCODE_MODEL", "")

	tests := []struct {
		name    string
		buildFn buildFunc
		prompt  string
		// wantArgs is the full expected cmd.Args slice (including binary name).
		wantArgs []string
	}{
		{
			name:     "claude",
			buildFn:  buildClaudeInteractiveCmd,
			prompt:   "do something",
			wantArgs: []string{"claude", "--dangerously-skip-permissions", "do something"},
		},
		{
			name:     "codex",
			buildFn:  buildCodexInteractiveCmd,
			prompt:   "do something",
			wantArgs: []string{"codex", "--no-alt-screen", "--dangerously-bypass-approvals-and-sandbox", "do something"},
		},
		{
			name:     "opencode",
			buildFn:  buildOpenCodeInteractiveCmd,
			prompt:   "do something",
			wantArgs: []string{"opencode", "run", "--dir", "/tmp/work", "--dangerously-skip-permissions", "do something"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.buildFn("/tmp/work", tc.prompt, "test-agent")
			if len(cmd.Args) != len(tc.wantArgs) {
				t.Fatalf("args length mismatch: got %v, want %v", cmd.Args, tc.wantArgs)
			}
			for i, arg := range tc.wantArgs {
				if cmd.Args[i] != arg {
					t.Errorf("args[%d] = %q, want %q", i, cmd.Args[i], arg)
				}
			}
		})
	}
}

func TestBuildInteractiveCmd_StdinIsOsStdin(t *testing.T) {
	tests := []struct {
		name    string
		buildFn buildFunc
	}{
		{"claude", buildClaudeInteractiveCmd},
		{"codex", buildCodexInteractiveCmd},
		{"opencode", buildOpenCodeInteractiveCmd},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.buildFn("/tmp/work", "test prompt", "agent")
			if cmd.Stdin != os.Stdin {
				t.Errorf("cmd.Stdin is not os.Stdin (got %v, want %v)", cmd.Stdin, os.Stdin)
			}
		})
	}
}

func TestBuildInteractiveCmd_BinaryAndFlags(t *testing.T) {
	tests := []struct {
		name       string
		buildFn    buildFunc
		wantBinary string
		wantFlags  []string // flags expected in Args (excluding binary and prompt)
	}{
		{
			name:       "claude",
			buildFn:    buildClaudeInteractiveCmd,
			wantBinary: "claude",
			wantFlags:  []string{"--dangerously-skip-permissions"},
		},
		{
			name:       "codex",
			buildFn:    buildCodexInteractiveCmd,
			wantBinary: "codex",
			wantFlags:  []string{"--no-alt-screen", "--dangerously-bypass-approvals-and-sandbox"},
		},
		{
			name:       "opencode",
			buildFn:    buildOpenCodeInteractiveCmd,
			wantBinary: "opencode",
			wantFlags:  []string{"run", "--dir", "--dangerously-skip-permissions"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.buildFn("/tmp/work", "test prompt", "agent")
			if cmd.Args[0] != tc.wantBinary {
				t.Errorf("binary = %q, want %q", cmd.Args[0], tc.wantBinary)
			}
			for _, flag := range tc.wantFlags {
				found := false
				for _, arg := range cmd.Args {
					if arg == flag {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected flag %q in args %v", flag, cmd.Args)
				}
			}
		})
	}
}

func TestBuildInteractiveCmd_EnvVars(t *testing.T) {
	tests := []struct {
		name       string
		buildFn    buildFunc
		workDir    string
		agentName  string
		wantEnv    map[string]string // key -> expected value
		wantAbsent []string          // keys that must NOT be present
	}{
		{
			name:      "claude with agent",
			buildFn:   buildClaudeInteractiveCmd,
			workDir:   "/projects/myapp",
			agentName: "nova",
			wantEnv: map[string]string{
				"LOOM_WORKTREE_PATH": "/projects/myapp",
				"LOOM_AGENT_NAME":    "nova",
			},
		},
		{
			name:      "codex with agent",
			buildFn:   buildCodexInteractiveCmd,
			workDir:   "/projects/myapp",
			agentName: "nova",
			wantEnv: map[string]string{
				"LOOM_WORKTREE_PATH": "/projects/myapp",
				"LOOM_AGENT_NAME":    "nova",
			},
		},
		{
			name:      "opencode with agent",
			buildFn:   buildOpenCodeInteractiveCmd,
			workDir:   "/projects/myapp",
			agentName: "nova",
			wantEnv: map[string]string{
				"LOOM_WORKTREE_PATH": "/projects/myapp",
				"LOOM_AGENT_NAME":    "nova",
			},
		},
		{
			name:      "claude without agent",
			buildFn:   buildClaudeInteractiveCmd,
			workDir:   "/projects/myapp",
			agentName: "",
			wantEnv: map[string]string{
				"LOOM_WORKTREE_PATH": "/projects/myapp",
			},
			wantAbsent: []string{"LOOM_AGENT_NAME"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Unset LOOM_AGENT_NAME to avoid leaking from test environment.
			old, hadBD := os.LookupEnv("LOOM_AGENT_NAME")
			os.Unsetenv("LOOM_AGENT_NAME")
			t.Cleanup(func() {
				if hadBD {
					os.Setenv("LOOM_AGENT_NAME", old)
				}
			})

			cmd := tc.buildFn(tc.workDir, "test prompt", tc.agentName)
			for key, want := range tc.wantEnv {
				got, ok := envValue(cmd.Env, key)
				if !ok {
					t.Errorf("expected %s in env, not found", key)
					continue
				}
				if got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			for _, key := range tc.wantAbsent {
				if envHasKey(cmd.Env, key) {
					t.Errorf("expected %s to be absent from env, but it was present", key)
				}
			}
		})
	}
}

func TestBuildInteractiveCmd_TermDumbInTmux(t *testing.T) {
	// TERM=dumb is set by automode.go for claude sessions in tmux.
	// The backends pass through whatever TERM is in the process environment
	// via FilteredEnv(). This test confirms that propagation path works.

	tests := []struct {
		name    string
		buildFn buildFunc
	}{
		{"claude", buildClaudeInteractiveCmd},
		{"codex", buildCodexInteractiveCmd},
		{"opencode", buildOpenCodeInteractiveCmd},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldTerm, hadTerm := os.LookupEnv("TERM")
			os.Setenv("TERM", "dumb")
			t.Cleanup(func() {
				if hadTerm {
					os.Setenv("TERM", oldTerm)
				} else {
					os.Unsetenv("TERM")
				}
			})

			cmd := tc.buildFn("/tmp/work", "test prompt", "agent")
			got, ok := envValue(cmd.Env, "TERM")
			if !ok {
				t.Fatal("expected TERM in env, not found")
			}
			if got != "dumb" {
				t.Errorf("TERM = %q, want %q", got, "dumb")
			}
		})
	}
}

func TestBuildClaudeNonInteractiveCmd_BudgetInArgs(t *testing.T) {
	// Not parallel: mutates env vars via t.Setenv.

	t.Run("default budget included", func(t *testing.T) {
		// Use t.Setenv to register cleanup, then unset so the var is truly absent.
		t.Setenv("LOOM_MAX_BUDGET_USD", "placeholder")
		os.Unsetenv("LOOM_MAX_BUDGET_USD")

		cmd := buildClaudeNonInteractiveCmd("/tmp/work", "agent", "")
		args := cmd.Args

		// Find --max-budget-usd in args
		found := false
		for i, arg := range args {
			if arg == "--max-budget-usd" {
				found = true
				if i+1 >= len(args) {
					t.Fatal("--max-budget-usd flag present but no value follows")
				}
				if args[i+1] != "5.00" {
					t.Errorf("--max-budget-usd value = %q, want %q", args[i+1], "5.00")
				}
				break
			}
		}
		if !found {
			t.Errorf("expected --max-budget-usd in args %v", args)
		}
	})

	t.Run("opt-out with zero omits flag", func(t *testing.T) {
		t.Setenv("LOOM_MAX_BUDGET_USD", "0")

		cmd := buildClaudeNonInteractiveCmd("/tmp/work", "agent", "")
		args := cmd.Args

		for _, arg := range args {
			if arg == "--max-budget-usd" {
				t.Errorf("expected --max-budget-usd to be absent when LOOM_MAX_BUDGET_USD=0, got args %v", args)
				break
			}
		}
	})

	t.Run("custom budget value", func(t *testing.T) {
		t.Setenv("LOOM_MAX_BUDGET_USD", "12.34")

		cmd := buildClaudeNonInteractiveCmd("/tmp/work", "agent", "")
		args := cmd.Args

		found := false
		for i, arg := range args {
			if arg == "--max-budget-usd" {
				found = true
				if i+1 >= len(args) {
					t.Fatal("--max-budget-usd flag present but no value follows")
				}
				if args[i+1] != "12.34" {
					t.Errorf("--max-budget-usd value = %q, want %q", args[i+1], "12.34")
				}
				break
			}
		}
		if !found {
			t.Errorf("expected --max-budget-usd in args %v", args)
		}
	})
}

func TestBuildInteractiveCmd_WorkDir(t *testing.T) {
	tests := []struct {
		name    string
		buildFn buildFunc
	}{
		{"claude", buildClaudeInteractiveCmd},
		{"codex", buildCodexInteractiveCmd},
		{"opencode", buildOpenCodeInteractiveCmd},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workDir := "/projects/myapp"
			cmd := tc.buildFn(workDir, "test prompt", "agent")
			if cmd.Dir != workDir {
				t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, workDir)
			}
		})
	}
}

func TestBuildOpenCodeInteractiveCmd_PassesDirFlag(t *testing.T) {
	t.Setenv("LOOM_OPENCODE_MODEL", "")

	workDir := "/projects/myapp"
	cmd := buildOpenCodeInteractiveCmd(workDir, "test prompt", "agent")

	for i, arg := range cmd.Args {
		if arg == "--dir" {
			if i+1 >= len(cmd.Args) {
				t.Fatal("--dir flag present but no value follows")
			}
			if cmd.Args[i+1] != workDir {
				t.Fatalf("--dir value = %q, want %q", cmd.Args[i+1], workDir)
			}
			return
		}
	}
	t.Fatalf("expected --dir flag in args %v", cmd.Args)
}

func TestBuildOpenCodeInteractiveCmd_PassesModelFlagFromEnv(t *testing.T) {
	workDir := "/projects/myapp"
	model := "openai/gpt-5.4-mini"
	t.Setenv("LOOM_OPENCODE_MODEL", model)

	cmd := buildOpenCodeInteractiveCmd(workDir, "test prompt", "agent")
	for i, arg := range cmd.Args {
		if arg == "--model" {
			if i+1 >= len(cmd.Args) {
				t.Fatal("--model flag present but no value follows")
			}
			if cmd.Args[i+1] != model {
				t.Fatalf("--model value = %q, want %q", cmd.Args[i+1], model)
			}
			return
		}
	}
	t.Fatalf("expected --model flag in args %v", cmd.Args)
}
