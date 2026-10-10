package loomagent

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestNoSleepInTests fails on a sleep from package time in any test file
// under internal/loomagent, unless testdata/sleep-allowlist.txt names its
// file and enclosing func; an entry that matches no sleep fails too. A test
// that needs a timeout to pass is wrong: drain or await the persisted event.
func TestNoSleepInTests(t *testing.T) {
	allowed := map[string]bool{}
	f, err := os.Open("testdata/sleep-allowlist.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for sc := bufio.NewScanner(f); sc.Scan(); {
		if line, _, _ := strings.Cut(sc.Text(), "#"); strings.TrimSpace(line) != "" {
			allowed[strings.Join(strings.Fields(line), " ")] = true
		}
	}
	used := map[string]bool{}
	var bad []string
	err = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		timePkg := ""
		for _, im := range file.Imports {
			if p, _ := strconv.Unquote(im.Path.Value); p == "time" {
				timePkg = "time"
				if im.Name != nil {
					timePkg = im.Name.Name
				}
			}
		}
		if timePkg == "" {
			return nil
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Sleep" {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == timePkg {
					key := filepath.ToSlash(path) + " " + fn.Name.Name
					if used[key] = true; !allowed[key] {
						bad = append(bad, fset.Position(sel.Pos()).String()+" in "+fn.Name.Name)
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key := range allowed {
		if !used[key] {
			bad = append(bad, "stale allowlist entry: "+key)
		}
	}
	slices.Sort(bad)
	if len(bad) > 0 {
		t.Fatalf("sleeps in loomagent tests (drain the loops or await the persisted event instead; see testdata/sleep-allowlist.txt):\n%s",
			strings.Join(bad, "\n"))
	}
}
