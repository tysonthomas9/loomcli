package runtimepreflight_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAutomaticGateProbeSeams(t *testing.T) {
	repoRoot := preflightRepoRoot(t)
	queueGates := []string{
		"internal/cli/epic/run.go",
		"internal/cli/workflow/workflow_cmd.go",
		"internal/webui/handlers/workflows/preflight.go",
	}
	for _, relativePath := range queueGates {
		t.Run(relativePath, func(t *testing.T) {
			calls := parsedCallNames(t, filepath.Join(repoRoot, relativePath))
			if calls["RequireLocalTaskRunner"] == 0 {
				t.Fatalf("%s does not call RequireLocalTaskRunner", relativePath)
			}
			for _, forbidden := range []string{"RequireLocalTaskRunnerForAdmission", "CheckLocalTaskRunnerForAdmission"} {
				if calls[forbidden] != 0 {
					t.Fatalf("%s calls launch-only %s", relativePath, forbidden)
				}
			}
		})
	}

	t.Run("tsruntime launch gate", func(t *testing.T) {
		relativePath := "internal/cli/agent/tsruntime/tsruntime.go"
		calls := parsedCallNames(t, filepath.Join(repoRoot, relativePath))
		admissionCalls := calls["RequireLocalTaskRunnerForAdmission"] + calls["CheckLocalTaskRunnerForAdmission"]
		if admissionCalls == 0 {
			t.Fatalf("%s does not call an admission preflight variant", relativePath)
		}
		if calls["RequireLocalTaskRunner"] != 0 || calls["CheckLocalTaskRunner"] != 0 {
			t.Fatalf("%s calls a full preflight variant: %+v", relativePath, calls)
		}
	})

	t.Run("host bridge launch checker", func(t *testing.T) {
		relativePath := "internal/runtimepreflight/preflight.go"
		calls := parsedCallNames(t, filepath.Join(repoRoot, relativePath))
		if calls["CheckLocalTaskRunnerForAdmission"] < 2 {
			t.Fatalf("%s admission check calls = %d, want at least 2 to cover the admission projection and launch checker", relativePath, calls["CheckLocalTaskRunnerForAdmission"])
		}
	})
}

func parsedCallNames(t *testing.T, sourcePath string) map[string]int {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), sourcePath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", sourcePath, err)
	}
	calls := make(map[string]int)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch callee := call.Fun.(type) {
		case *ast.Ident:
			calls[callee.Name]++
		case *ast.SelectorExpr:
			calls[callee.Sel.Name]++
		}
		return true
	})
	return calls
}

func preflightRepoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve preflight seam test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
