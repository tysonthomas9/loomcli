package cli

// Guards the boundary between this CLI's core and the agentic layer that runs
// on top of it: workspace label vocabulary belongs to the workspace, not to a
// compiled binary. Six PUPPET label names were compiled in over three days in
// September 2026 — not by decision, but because no config surface was at hand
// when the code was written. A rule that lives only in review comments does not
// survive a deadline, so it lives here instead.
//
// The rule: a label literal may exist under internal/ only if it is
//
//   - prefixed "loom:" — a mark the supervisor writes about its own runs
//     (loom:attempt:, loom:quarantined, ...); or
//   - on the allowlist below.
//
// Everything else — pipeline rungs, ledger markers, routing terms — is the
// agentic layer's vocabulary and must arrive through config.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// allowedLabelLiterals are the only bare label names the core may name itself.
//
//	operator       fleet-db enforces it server-side, at ready-queue eligibility
//	               and in the claim guard, so the CLI must agree on the spelling.
//	needs-revision this CLI's own plan-review flow; written by the web UI.
//	review-cycle=  the default counter-label prefix of the CLI's own generic
//	               review-cycle mechanism, overridable per hook in config. The
//	               name is the core's, not a workspace's.
var allowedLabelLiterals = map[string]bool{
	"operator":       true,
	"needs-revision": true,
	"review-cycle=":  true,
}

// loomLabelPrefix marks labels the supervisor writes about its own runs.
const loomLabelPrefix = "loom:"

// labelIdentRe matches the identifiers whose string values are labels.
var labelIdentRe = regexp.MustCompile(`(?i)label`)

// labelValueRe is the shape of an issue-label name: lowercase alphanumeric
// words joined by "-" or ":", optionally ending in a separator because label
// PREFIXES ("union-debt-of:", "review-cycle=") are label literals too. It is
// what keeps this guard off the many other things Go code calls a "label" —
// launchd labels, UI captions, RPC op names, event names, process-arg keys —
// none of which can be spelled this way.
var labelValueRe = regexp.MustCompile(`^[a-z0-9]+(?:[-:][a-z0-9]+)*[-:=]?$`)

// nonLabelIdentRe excludes identifiers that merely read as labels. Comment
// dedupe markers ("loom-abandoned-run:", "loom-timeout-run:") are prefixes for
// comment bodies, not labels; they are excluded by identifier name rather than
// by widening the allowlist above, which would license them as labels.
var nonLabelIdentRe = regexp.MustCompile(`(?i)marker$`)

func TestLabelBoundary(t *testing.T) {
	root := repoRoot(t)

	var findings []string
	fset := token.NewFileSet()

	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "vendor" || name == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") ||
			strings.HasSuffix(name, ".gen.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		for _, v := range labelLiterals(file) {
			if isAllowedLabel(v.value) {
				continue
			}
			findings = append(findings, formatFinding(rel, fset.Position(v.pos).Line, v.ident, v.value))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}

	if len(findings) > 0 {
		sort.Strings(findings)
		t.Fatalf("workspace label literals compiled into the core:\n\n%s\n\n%s",
			strings.Join(findings, "\n"), labelBoundaryRule())
	}
}

// labelBoundaryRule is the signpost the failure prints: the rule, and the two
// legal ways out of it.
func labelBoundaryRule() string {
	allowed := make([]string, 0, len(allowedLabelLiterals))
	for v := range allowedLabelLiterals {
		allowed = append(allowed, strconv.Quote(v))
	}
	sort.Strings(allowed)
	return `The rule: a label literal under internal/ must be either
  - prefixed "` + loomLabelPrefix + `" (a mark the supervisor writes about its own runs), or
  - on the allowlist in internal/cli/labelboundary_test.go (currently
    ` + strings.Join(allowed, ", ") + `).

Every other label name belongs to the agentic layer that runs on this CLI, and
must reach the code through config (the workspace's integration.yaml) rather
than being compiled in. Adding it to the allowlist is not the fix unless the
core itself genuinely owns the name; read the allowlist comments first.

If the identifier is not really a label — a comment dedupe marker, say — rename
it to end in "Marker", which this guard skips.`
}

// labelLiteral is one string literal that reached a label-shaped identifier.
type labelLiteral struct {
	ident string
	value string
	pos   token.Pos
}

// labelLiterals collects string literals assigned to, keyed by, or compared
// against an identifier whose name mentions "label".
func labelLiterals(file *ast.File) []labelLiteral {
	var out []labelLiteral
	add := func(nameExpr ast.Expr, valueExpr ast.Expr) {
		name, ok := identName(nameExpr)
		if !ok || !labelIdentRe.MatchString(name) || nonLabelIdentRe.MatchString(name) {
			return
		}
		value, ok := stringLit(valueExpr)
		if !ok {
			return
		}
		out = append(out, labelLiteral{ident: name, value: value, pos: valueExpr.Pos()})
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				if i < len(node.Rhs) {
					add(lhs, node.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			for i, name := range node.Names {
				if i < len(node.Values) {
					add(name, node.Values[i])
				}
			}
		case *ast.KeyValueExpr:
			add(node.Key, node.Value)
		case *ast.BinaryExpr:
			if node.Op != token.EQL && node.Op != token.NEQ {
				return true
			}
			add(node.X, node.Y)
			add(node.Y, node.X)
		}
		return true
	})
	return out
}

// identName returns the trailing identifier of an ident or selector expression.
func identName(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name, true
	case *ast.SelectorExpr:
		return v.Sel.Name, true
	case *ast.IndexExpr:
		return identName(v.X)
	}
	return "", false
}

// stringLit returns the value of an untagged string literal.
func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

// isAllowedLabel reports whether a label literal may be compiled into the core.
// The empty string is a cleared value, not a label name.
func isAllowedLabel(v string) bool {
	if v == "" || !labelValueRe.MatchString(v) {
		return true
	}
	return strings.HasPrefix(v, loomLabelPrefix) || allowedLabelLiterals[v]
}

func formatFinding(file string, line int, ident, value string) string {
	return "  " + file + ":" + strconv.Itoa(line) + ": " + strconv.Quote(value) +
		" reaches label-shaped identifier " + strconv.Quote(ident)
}

// repoRoot walks up from the test's directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}
