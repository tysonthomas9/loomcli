package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Route is a single method+path registration found in Go source.
type Route struct {
	Method string
	Path   string
	File   string // repo-relative
	// Line is where the pattern literal sits. Deliberately NOT rendered into
	// docs/api.md: that file is staleness-gated, so a line number would make
	// any edit above a registration (an import, a doc comment) fail the gate
	// with no API change. Kept because it is useful to callers and tests.
	Line int
}

// routePattern matches a Go 1.22+ ServeMux pattern literal that carries an
// explicit method, e.g. "GET /api/workspaces/{ws}/issues".
var routePattern = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) (/\S*)$`)

// mountPattern matches a method-less subtree mount, e.g. "/api/auth/". Those
// are not endpoints, but an opaque one (a mount with no enumerable children,
// such as the BetterAuth reverse proxy) still serves every path beneath it.
var mountPattern = regexp.MustCompile(`^(/\S*/)$`)

// scanRoutes walks root for non-test Go files and returns every ServeMux route
// registration it finds, sorted by path then method.
//
// Detection rule: a call expression whose callee name contains "handle"
// (case-insensitive) and whose first argument is a string literal matching
// routePattern. That covers `mux.Handle`, `mux.HandleFunc`, and the local
// `handle(...)` closures used by handler modules that wrap every route in
// shared middleware (internal/webui/handlers/misc/module.go:31).
//
// This is a syntactic scan, not a runtime enumeration. It cannot see routes
// built from non-literal patterns, and it reports routes that are registered
// conditionally (many modules skip registration when their service dependency
// is nil) as though they were always present.
func scanRoutes(root string, repoRoot string) (routes, mounts []Route, err error) {
	files, err := goSourceFiles(root)
	if err != nil {
		return nil, nil, err
	}

	fset := token.NewFileSet()
	seen := make(map[string]bool)
	for _, path := range files {
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", path, perr)
		}
		rel, rerr := filepath.Rel(repoRoot, path)
		if rerr != nil {
			rel = path
		}
		ast.Inspect(file, func(n ast.Node) bool {
			found, ok := routeFromCall(n, fset, filepath.ToSlash(rel))
			if !ok {
				return true
			}
			key := found.Method + " " + found.Path
			if seen[key] {
				return true
			}
			seen[key] = true
			if found.Method == "" {
				mounts = append(mounts, found)
			} else {
				routes = append(routes, found)
			}
			return true
		})
	}

	sortRoutes(routes)
	sortRoutes(mounts)
	return routes, mounts, nil
}

// goSourceFiles lists non-test Go files under root, sorted for determinism.
func goSourceFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	sort.Strings(files)
	return files, nil
}

// routeFromCall extracts a route or subtree mount from an AST node, reporting
// false when the node is not a route registration.
func routeFromCall(n ast.Node, fset *token.FileSet, relPath string) (Route, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 || !isHandlerCall(call.Fun) {
		return Route{}, false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return Route{}, false
	}
	val, err := strconv.Unquote(lit.Value)
	if err != nil {
		return Route{}, false
	}
	found := Route{File: relPath, Line: fset.Position(lit.Pos()).Line}
	switch m := routePattern.FindStringSubmatch(val); {
	case m != nil:
		found.Method, found.Path = m[1], m[2]
	case mountPattern.MatchString(val):
		found.Path = val
	default:
		return Route{}, false
	}
	return found, true
}

// sortRoutes orders routes by path, then by canonical method order.
func sortRoutes(rs []Route) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Path != rs[j].Path {
			return rs[i].Path < rs[j].Path
		}
		return methodRank(rs[i].Method) < methodRank(rs[j].Method)
	})
}

// isHandlerCall reports whether the callee looks like a route registration.
func isHandlerCall(fun ast.Expr) bool {
	var name string
	switch f := fun.(type) {
	case *ast.Ident:
		name = f.Name
	case *ast.SelectorExpr:
		name = f.Sel.Name
	default:
		return false
	}
	return strings.Contains(strings.ToLower(name), "handle")
}

// methodRank orders HTTP methods canonically instead of alphabetically.
func methodRank(m string) int {
	for i, want := range methodOrder {
		if m == want {
			return i
		}
	}
	return len(methodOrder)
}

// pathParamRe matches a `{name}` path parameter placeholder.
var pathParamRe = regexp.MustCompile(`\{[^}]*\}`)

// normalizePath erases path-parameter names so that a spec path and a route
// pattern that differ only in placeholder naming compare equal. The spec calls
// the issue placeholder `{id}` on some paths and `{issueId}` on others; the Go
// route uses whichever the handler reads.
func normalizePath(p string) string {
	return pathParamRe.ReplaceAllString(p, "{}")
}

// MountedOperation is a spec operation with no route of its own that is
// nonetheless served by an opaque subtree mount.
type MountedOperation struct {
	SpecOperation
	Mount Route
}

// Drift is the comparison between the spec and the registered routes.
type Drift struct {
	// UndocumentedRoutes are registered in Go but absent from the spec.
	UndocumentedRoutes []Route
	// PhantomPaths are documented in the spec but have no registration.
	PhantomPaths []SpecOperation
	// MountedPaths are documented, have no route of their own, but fall under
	// an opaque subtree mount that forwards everything beneath it.
	MountedPaths []MountedOperation
	// Matched counts operations present on both sides.
	Matched int
	// RouteCount is the number of method-ful registrations scanned.
	RouteCount int
}

// compareRoutes diffs spec operations against scanned route registrations.
// mounts are method-less subtree registrations; only the opaque ones (no
// method-ful route registered beneath them) can account for a spec path.
func compareRoutes(ops []SpecOperation, routes, mounts []Route) Drift {
	specKeys := make(map[string]bool, len(ops))
	for _, o := range ops {
		specKeys[o.Method+" "+normalizePath(o.Path)] = true
	}
	routeKeys := make(map[string]bool, len(routes))
	for _, r := range routes {
		routeKeys[r.Method+" "+normalizePath(r.Path)] = true
	}
	opaque := opaqueMounts(routes, mounts)

	d := Drift{RouteCount: len(routes)}
	for _, r := range routes {
		if specKeys[r.Method+" "+normalizePath(r.Path)] {
			d.Matched++
			continue
		}
		d.UndocumentedRoutes = append(d.UndocumentedRoutes, r)
	}
	for _, o := range ops {
		if routeKeys[o.Method+" "+normalizePath(o.Path)] {
			continue
		}
		if mount, ok := coveringMount(o.Path, opaque); ok {
			d.MountedPaths = append(d.MountedPaths, MountedOperation{SpecOperation: o, Mount: mount})
			continue
		}
		d.PhantomPaths = append(d.PhantomPaths, o)
	}
	sortSpecOps(d.PhantomPaths)
	sort.SliceStable(d.MountedPaths, func(i, j int) bool {
		if d.MountedPaths[i].Path != d.MountedPaths[j].Path {
			return d.MountedPaths[i].Path < d.MountedPaths[j].Path
		}
		return methodRank(d.MountedPaths[i].Method) < methodRank(d.MountedPaths[j].Method)
	})
	return d
}

// opaqueMounts keeps only subtree mounts that have no method-ful route beneath
// them. A mount with enumerable children (`/api/workspaces/{ws}/`, the JSON-404
// catch-all `/api/`, the SPA fallback `/`) explains nothing that the individual
// registrations do not already explain, and treating it as a wildcard would
// mask every real gap.
func opaqueMounts(routes, mounts []Route) []Route {
	var out []Route
	for _, m := range mounts {
		prefix := normalizePath(m.Path)
		hasChild := false
		for _, r := range routes {
			if strings.HasPrefix(normalizePath(r.Path), prefix) {
				hasChild = true
				break
			}
		}
		if !hasChild {
			out = append(out, m)
		}
	}
	return out
}

// coveringMount returns the opaque mount that serves path, if any.
func coveringMount(path string, opaque []Route) (Route, bool) {
	norm := normalizePath(path)
	for _, m := range opaque {
		if strings.HasPrefix(norm, normalizePath(m.Path)) {
			return m, true
		}
	}
	return Route{}, false
}

// sortSpecOps orders spec operations by path, then canonical method order.
func sortSpecOps(ops []SpecOperation) {
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return methodRank(ops[i].Method) < methodRank(ops[j].Method)
	})
}
