package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// envVar aggregates every git-tracked reference to a single LOOM_* environment
// variable: the os-package operations it is read through, the packages that read
// it, and the packages that declare its name as a Go constant. One row of the
// generated reference is built from one of these.
type envVar struct {
	name string
	// ops maps an os operation name ("Getenv", "LookupEnv", "Setenv") to the
	// number of direct call sites that read this variable through it.
	ops map[string]int
	// readPkgs is the set of packages (repo-relative dirs) with a direct read.
	readPkgs map[string]bool
	// declPkgs is the set of packages that declare a string constant whose value
	// is this variable name. Populated for every variable; the sole provenance
	// for ones read only indirectly (through a wrapper), which have no ops.
	declPkgs map[string]bool
}

// generateEnvVars renders the reference of every LOOM_* environment variable the
// git-tracked Go source reads. It derives the inventory from two signals, both
// resolved through go/types so a variable named by a constant counts the same as
// one named by a string literal:
//
//   - Direct reads: calls to os.Getenv, os.LookupEnv, and os.Setenv whose first
//     argument resolves to a string constant. os.Getenv("LOOM_X") and
//     os.Getenv(bootstrap.EnvFleetDBURL) both collapse to the underlying name; a
//     plain grep for "LOOM_" would miss every constant-named read.
//   - Indirect reads: LOOM_* names declared as string constants. Some variables
//     are only ever read through a wrapper (baseURLOverride and boundedIntEnv
//     read os.Getenv(param), so the name is not a constant at the call site).
//     Scanning constant declarations recovers those names; they are marked
//     "indirect" because no direct call site can be attributed to them.
//
// Provenance is package-level, not file:line — the same discipline the sibling
// openapi-to-md generator follows: this document is meant to be staleness-gated,
// and a line number would make any edit above a read fail the gate with no change
// to the environment contract.
//
// Every collection is sorted before rendering; no map is ranged for output
// order. Rationale a human maintains lives in docs/reference/env-vars.preamble.md.
func generateEnvVars(cfg *genConfig) (string, error) {
	pkgs, err := cfg.Packages()
	if err != nil {
		return "", err
	}

	vars := map[string]*envVar{}
	touch := func(name string) *envVar {
		v := vars[name]
		if v == nil {
			v = &envVar{
				name:     name,
				ops:      map[string]int{},
				readPkgs: map[string]bool{},
				declPkgs: map[string]bool{},
			}
			vars[name] = v
		}
		return v
	}

	for _, pkg := range pkgs {
		info := pkg.TypesInfo
		if info == nil || pkg.Fset == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			rel, ok := repoRelDir(cfg, pkg.Fset, file)
			if !ok {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CallExpr:
					collectRead(node, info, rel, touch)
				case *ast.GenDecl:
					collectConstDecl(node, info, rel, touch)
				}
				return true
			})
		}
	}

	return renderEnvVars(vars), nil
}

// repoRelDir returns the repo-relative package directory (slash-separated) of a
// parsed file, reporting false when the file is outside the module or not
// git-tracked. Packages() already filters to tracked packages, so the tracked
// check is defense in depth against a stray generated file.
func repoRelDir(cfg *genConfig, fset *token.FileSet, file *ast.File) (string, bool) {
	abs := fset.Position(file.Pos()).Filename
	rel, err := filepath.Rel(cfg.RepoRoot, abs)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if cfg.Ignored(rel) {
		return "", false
	}
	return path.Dir(rel), true
}

// collectRead records a direct read when call is os.Getenv/os.LookupEnv/os.Setenv
// with a first argument that resolves to a LOOM_* string constant.
func collectRead(call *ast.CallExpr, info *types.Info, pkgDir string, touch func(string) *envVar) {
	if len(call.Args) == 0 {
		return
	}
	op, ok := osEnvOp(call.Fun, info)
	if !ok {
		return
	}
	name, ok := stringConst(call.Args[0], info)
	if !ok || !isLoomEnvName(name) {
		return
	}
	v := touch(name)
	v.ops[op]++
	v.readPkgs[pkgDir] = true
}

// collectConstDecl records every string constant in a const declaration whose
// value is a LOOM_* environment-variable name, capturing the declaring package
// as provenance for reads that only happen through a wrapper.
func collectConstDecl(decl *ast.GenDecl, info *types.Info, pkgDir string, touch func(string) *envVar) {
	if decl.Tok != token.CONST {
		return
	}
	for _, spec := range decl.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, id := range vs.Names {
			c, ok := info.Defs[id].(*types.Const)
			if !ok || c.Val().Kind() != constant.String {
				continue
			}
			val := constant.StringVal(c.Val())
			if !isLoomEnvName(val) {
				continue
			}
			touch(val).declPkgs[pkgDir] = true
		}
	}
}

// osEnvOp reports the operation name when fun is a call to os.Getenv, os.LookupEnv
// or os.Setenv, verified through type info so a local identifier shadowing "os"
// cannot be mistaken for the standard library.
func osEnvOp(fun ast.Expr, info *types.Info) (string, bool) {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch sel.Sel.Name {
	case "Getenv", "LookupEnv", "Setenv":
	default:
		return "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	pkgName, ok := info.Uses[id].(*types.PkgName)
	if !ok || pkgName.Imported().Path() != "os" {
		return "", false
	}
	return sel.Sel.Name, true
}

// stringConst resolves an expression to its string-constant value. This is what
// collapses both literals (os.Getenv("LOOM_X")) and named constants
// (os.Getenv(bootstrap.EnvFleetDBURL)) to the underlying variable name; a
// non-constant argument (a wrapper parameter or loop variable) has no value and
// is reported as not resolvable.
func stringConst(arg ast.Expr, info *types.Info) (string, bool) {
	tv, ok := info.Types[arg]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

// isLoomEnvName reports whether s is a full LOOM_* environment-variable name: the
// LOOM_ prefix, at least one more character, all uppercase/digit/underscore, and
// not ending in "_" (which would be a concatenation prefix such as LOOM_AGENT_
// or the bare LOOM_, never a real variable).
func isLoomEnvName(s string) bool {
	if !strings.HasPrefix(s, "LOOM_") || len(s) <= len("LOOM_") || strings.HasSuffix(s, "_") {
		return false
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// subsystemOf reduces a package directory to its subsystem: the first two path
// segments (internal/cli, internal/driver, cmd/loom), which is the "top-level
// internal/ package" the reference groups by.
func subsystemOf(pkgDir string) string {
	parts := strings.Split(pkgDir, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return pkgDir
}

// siteCount is the total number of direct call sites reading this variable.
func (v *envVar) siteCount() int {
	n := 0
	for _, c := range v.ops {
		n += c
	}
	return n
}

// access renders the sorted set of os operations the variable is read through,
// or "indirect" when it is only declared as a constant and read via a wrapper.
func (v *envVar) access() string {
	if len(v.ops) == 0 {
		return "indirect"
	}
	return strings.Join(envSortedKeys(v.ops), ", ")
}

// readIn renders the sorted, backticked packages that read the variable. For a
// variable read only indirectly it falls back to the declaring package(s),
// tagged so the distinction is not lost.
func (v *envVar) readIn() string {
	src, suffix := v.readPkgs, ""
	if len(src) == 0 {
		src, suffix = v.declPkgs, " (declared)"
	}
	return joinBackticked(envSortedKeys(src)) + suffix
}

// group is the subsystem the variable is listed under: its declaring subsystem
// when it is a named constant, else the lexicographically first reading
// subsystem. Deterministic and independent of map iteration order.
func (v *envVar) group() string {
	src := v.declPkgs
	if len(src) == 0 {
		src = v.readPkgs
	}
	best := ""
	for pkgDir := range src {
		if s := subsystemOf(pkgDir); best == "" || s < best {
			best = s
		}
	}
	if best == "" {
		best = "internal"
	}
	return best
}

// sensitive flags a variable whose name evidently carries a credential, using
// only fragments visible in the name (no semantic judgement about the code).
func (v *envVar) sensitive() bool {
	u := strings.ToUpper(v.name)
	for _, frag := range []string{"SECRET", "TOKEN", "PASSWORD", "KEY"} {
		if strings.Contains(u, frag) {
			return true
		}
	}
	return false
}

// envStats are the corpus-wide totals the summary line reports. Counted once,
// so the summary cannot drift from the tables below it.
type envStats struct {
	totalSites int            // direct read/set call sites across every variable
	readerPkgs int            // distinct packages containing at least one read
	opTotals   map[string]int // call sites per os operation (Getenv/LookupEnv/Setenv)
	indirect   int            // variables with no direct site (constant read via a wrapper)
	sensitive  int            // variables whose name matches the sensitivity heuristic
}

// envAggregate buckets the variables by subsystem and totals the corpus in one
// pass. names must already be sorted, which is what makes each group's order
// deterministic.
func envAggregate(vars map[string]*envVar, names []string) (map[string][]*envVar, envStats) {
	groups := map[string][]*envVar{}
	readerPkgs := map[string]bool{}
	stats := envStats{opTotals: map[string]int{}}
	for _, n := range names {
		v := vars[n]
		g := v.group()
		groups[g] = append(groups[g], v)
		for pkgDir := range v.readPkgs {
			readerPkgs[pkgDir] = true
		}
		for op, c := range v.ops {
			stats.opTotals[op] += c
		}
		stats.totalSites += v.siteCount()
		if v.siteCount() == 0 {
			stats.indirect++
		}
		if v.sensitive() {
			stats.sensitive++
		}
	}
	stats.readerPkgs = len(readerPkgs)
	return groups, stats
}

// renderEnvVars turns the collected variables into the deterministic markdown
// body: a factual summary line, then one table per subsystem, variables sorted
// by name.
func renderEnvVars(vars map[string]*envVar) string {
	names := envSortedKeys(vars)
	groups, stats := envAggregate(vars, names)

	var b strings.Builder
	line := func(format string, args ...any) {
		if len(args) == 0 {
			b.WriteString(format)
		} else {
			fmt.Fprintf(&b, format, args...)
		}
		b.WriteByte('\n')
	}

	line("## LOOM environment variables")
	line("")
	line("%d distinct `LOOM_*` variables, read at %d call sites across %d packages.",
		len(names), stats.totalSites, stats.readerPkgs)
	line("Direct reads by operation: Getenv %d, LookupEnv %d, Setenv %d.",
		stats.opTotals["Getenv"], stats.opTotals["LookupEnv"], stats.opTotals["Setenv"])
	line("%d read only indirectly (declared as a constant, read through a wrapper); %d name-flagged potentially sensitive.",
		stats.indirect, stats.sensitive)
	line("")

	for _, sub := range envSortedKeys(groups) {
		line("### %s", sub)
		line("")
		line("| Variable | Access | Sites | Sensitive | Read in |")
		line("|----------|--------|-------|-----------|---------|")
		for _, v := range groups[sub] {
			line("| `%s` | %s | %d | %s | %s |",
				v.name, v.access(), v.siteCount(), yesNoDash(v.sensitive()), v.readIn())
		}
		line("")
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

// envSortedKeys returns the keys of any string-keyed map in sorted order — the
// single chokepoint that makes every table, group, and cell built from a map
// deterministic regardless of Go's randomized map iteration.
func envSortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// joinBackticked wraps each element in backticks and joins with ", ".
func joinBackticked(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = "`" + s + "`"
	}
	return strings.Join(quoted, ", ")
}

// yesNoDash renders a boolean flag column: "yes" or an em dash.
func yesNoDash(b bool) string {
	if b {
		return "yes"
	}
	return "—"
}
