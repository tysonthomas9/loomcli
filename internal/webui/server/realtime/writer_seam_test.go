package realtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestProductionSSEFramingUsesWriter(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve writer seam test path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "../../../.."))
	writerPath := filepath.Join(filepath.Dir(testFile), "writer.go")
	fset := token.NewFileSet()

	for _, packageRoot := range []string{"internal", "cmd"} {
		root := filepath.Join(repoRoot, packageRoot)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || path == writerPath {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil || !containsSSEFramePrefix(value) {
					return true
				}
				rel, relErr := filepath.Rel(repoRoot, path)
				if relErr != nil {
					rel = path
				}
				t.Errorf("%s:%d contains direct SSE framing", rel, fset.Position(literal.Pos()).Line)
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk production Go packages under %s: %v", packageRoot, err)
		}
	}
}

func containsSSEFramePrefix(value string) bool {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	for _, line := range strings.Split(value, "\n") {
		if strings.HasPrefix(line, "id:") ||
			strings.HasPrefix(line, "event:") ||
			strings.HasPrefix(line, "data:") ||
			strings.HasPrefix(line, "retry:") ||
			(strings.HasPrefix(line, ":") && len(line) > 1 && strings.Contains(value, "\n")) {
			return true
		}
	}
	return false
}
