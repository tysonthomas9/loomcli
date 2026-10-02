// Command gitexecsites lists production Go files that start git through
// os/exec, as "path:count" lines. A call counts when the program argument of
// exec.Command or exec.CommandContext is the constant "git", however the
// context is written, the import is named or the call is wrapped.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var skipDirs = map[string]bool{"node_modules": true, "vendor": true, "third_party": true,
	".git": true, "worktrees": true, "dist": true, "testdata": true}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	counts, err := scan(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, line := range counts {
		fmt.Println(line)
	}
}

// scan returns sorted "path:count" lines for every non-test Go file under root.
func scan(root string) ([]string, error) {
	byDir := map[string][]*ast.File{}
	names := map[*ast.File]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		names[file] = filepath.ToSlash(rel)
		byDir[filepath.Dir(path)] = append(byDir[filepath.Dir(path)], file)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, files := range byDir {
		gitNames := gitConstants(files)
		for _, file := range files {
			if n := countCalls(file, gitNames); n > 0 {
				out = append(out, names[file]+":"+strconv.Itoa(n))
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// gitConstants collects identifiers in a package declared with the value "git".
func gitConstants(files []*ast.File) map[string]bool {
	found := map[string]bool{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, value := range spec.Values {
				if i < len(spec.Names) && isGit(value, nil) {
					found[spec.Names[i].Name] = true
				}
			}
			return true
		})
	}
	return found
}

func countCalls(file *ast.File, gitNames map[string]bool) int {
	execNames := map[string]bool{}
	for _, spec := range file.Imports {
		if spec.Path.Value == `"os/exec"` {
			name := "exec"
			if spec.Name != nil {
				name = spec.Name.Name
			}
			execNames[name] = true
		}
	}
	if len(execNames) == 0 {
		return 0
	}
	count := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || !execNames[pkg.Name] {
			return true
		}
		program := -1
		switch sel.Sel.Name {
		case "Command":
			program = 0
		case "CommandContext":
			program = 1
		}
		if program >= 0 && program < len(call.Args) && isGit(call.Args[program], gitNames) {
			count++
		}
		return true
	})
	return count
}

func isGit(expr ast.Expr, gitNames map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		value, err := strconv.Unquote(e.Value)
		return e.Kind == token.STRING && err == nil && value == "git"
	case *ast.Ident:
		return gitNames[e.Name]
	case *ast.ParenExpr:
		return isGit(e.X, gitNames)
	}
	return false
}
