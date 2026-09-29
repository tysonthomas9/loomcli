package backends

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
)

func TestRunControlledLeadRuntimeMatchesCanonicalBackendSet(t *testing.T) {
	sourcePath := "harness_lead_runtime.go"
	file, err := parser.ParseFile(token.NewFileSet(), sourcePath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", sourcePath, err)
	}
	handled := []string{backendnames.Codex}
	foundCodexSpecialCase := false
	foundHarnessInvocationCall := false
	ast.Inspect(file, func(node ast.Node) bool {
		fn, ok := node.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "harnessLeadInvocation" {
			return true
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				literal, ok := expr.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				backend, unquoteErr := strconv.Unquote(literal.Value)
				if unquoteErr != nil {
					t.Fatalf("unquote harness backend %s: %v", literal.Value, unquoteErr)
				}
				handled = append(handled, backend)
			}
			return true
		})
		return false
	})
	ast.Inspect(file, func(node ast.Node) bool {
		fn, ok := node.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "RunControlledLeadRuntime" {
			return true
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch expression := node.(type) {
			case *ast.BinaryExpr:
				if expression.Op != token.EQL {
					return true
				}
				left, leftOK := expression.X.(*ast.Ident)
				right, rightOK := expression.Y.(*ast.Ident)
				if leftOK && rightOK && left.Name == "backend" && right.Name == "NameCodex" {
					foundCodexSpecialCase = true
				}
			case *ast.CallExpr:
				callee, ok := expression.Fun.(*ast.Ident)
				if ok && callee.Name == "harnessLeadInvocation" {
					foundHarnessInvocationCall = true
				}
			}
			return true
		})
		return false
	})
	if !foundCodexSpecialCase {
		t.Fatal("RunControlledLeadRuntime no longer has the Codex special case")
	}
	if !foundHarnessInvocationCall {
		t.Fatal("RunControlledLeadRuntime no longer calls harnessLeadInvocation")
	}
	want := backendnames.ControlledLeadBackends()
	sort.Strings(handled)
	sort.Strings(want)
	if !slices.Equal(handled, want) {
		t.Fatalf("RunControlledLeadRuntime backends = %v, canonical set = %v", handled, want)
	}
}

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

func TestRunControlledLeadRuntimeDispatchesGenericBackends(t *testing.T) {
	expectations := map[string]struct {
		args           []string
		binary         string // backend name and exec binary differ for cursor (cursor-agent)
		dynamicSession bool
	}{
		backendnames.Claude:   {binary: "claude", dynamicSession: true},
		backendnames.Gemini:   {args: []string{"--approval-mode=yolo"}, binary: "gemini"},
		backendnames.Cursor:   {args: []string{"--force"}, binary: "cursor-agent"},
		backendnames.OpenCode: {binary: "opencode"},
	}
	for _, backend := range backendnames.ControlledLeadBackends() {
		if backend == backendnames.Codex {
			continue
		}
		want, ok := expectations[backend]
		if !ok {
			t.Fatalf("canonical backend %q has no behavioral dispatch expectation", backend)
		}
		t.Run(backend, func(t *testing.T) {
			captured := installFakeHarnessLead(t)
			handled, err := RunControlledLeadRuntime(context.Background(), nil, "WS", "nova", "lead-session", "/repo", "prompt", backend)
			if err != nil {
				t.Fatalf("RunControlledLeadRuntime() error = %v", err)
			}
			if !handled {
				t.Fatal("lead should be handled by the controlled runtime")
			}
			if captured.Backend != backend || captured.BinaryPath != want.binary {
				t.Fatalf("captured config = %+v", captured)
			}
			if want.dynamicSession {
				if len(captured.Args) != 3 || captured.Args[0] != "--session-id" || captured.Args[2] != "--dangerously-skip-permissions" {
					t.Fatalf("captured args = %q, want dynamic Claude session args", captured.Args)
				}
			} else if !slices.Equal(captured.Args, want.args) {
				t.Fatalf("captured args = %q, want %q", captured.Args, want.args)
			}
			if backend == backendnames.OpenCode && captured.PromptFlag != "--prompt" {
				t.Fatalf("PromptFlag = %q, want --prompt", captured.PromptFlag)
			}
			if backend == backendnames.Claude {
				return
			}
			for _, kv := range captured.Env {
				if strings.HasPrefix(kv, "CLAUDE_CODE_NO_FLICKER=") {
					t.Fatalf("Claude-only virtual scroll mode leaked into env: %#v", captured.Env)
				}
			}
		})
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
