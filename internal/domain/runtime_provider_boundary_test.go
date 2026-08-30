package domain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// selectableByDesign pins the client-selectable boundary explicitly, one entry
// per RuntimeProvider constant. It is duplicated from
// ClientSelectableRuntimeProvider ON PURPOSE: the predicate is a security
// boundary, so widening it must take two deliberate edits, not one.
var selectableByDesign = map[RuntimeProvider]bool{
	RuntimeProviderLocal:      true,
	RuntimeProviderE2B:        true,
	RuntimeProviderKubernetes: true,
	RuntimeProviderDaytona:    true,
	RuntimeProviderCI:         true,
	RuntimeProviderOther:      true,

	// A caller may POST {"runtime_provider":"exe"} and have the server
	// provision on it. The one boundary exe does NOT clear is egress policy,
	// which exe.dev cannot enforce; the adapter refuses a provision carrying an
	// allowlist rather than silently granting open egress.
	RuntimeProviderExe: true,
}

// sandboxPlacedByDesign is the second axis, declared per constant for the same
// reason as selectableByDesign: a missing entry must be a test failure, not a
// silent default. Getting this one wrong does not fail loudly -- it silently
// treats a sandboxed lead as local, so it is never provisioned, never
// attachable, or never reported.
var sandboxPlacedByDesign = map[RuntimeProvider]bool{
	RuntimeProviderDaytona: true,
	RuntimeProviderExe:     true,

	RuntimeProviderLocal:      false,
	RuntimeProviderE2B:        false,
	RuntimeProviderKubernetes: false,
	RuntimeProviderCI:         false,
	RuntimeProviderOther:      false,
}

func TestSandboxPlacedRuntimeProviderMatchesDesign(t *testing.T) {
	for provider, want := range sandboxPlacedByDesign {
		if got := SandboxPlacedRuntimeProvider(provider); got != want {
			t.Errorf("SandboxPlacedRuntimeProvider(%q) = %v, want %v", provider, got, want)
		}
	}
	for _, p := range []RuntimeProvider{"", "  ", "DAYTONA", "unknown"} {
		if SandboxPlacedRuntimeProvider(p) {
			t.Errorf("SandboxPlacedRuntimeProvider(%q) = true, want false (must fail closed)", p)
		}
	}
}

// TestRuntimeProviderAxesAreIndependent pins the distinction the two axes
// exist to express, since collapsing them is the easy mistake: "a caller may
// ask for it" and "its leads live in a remote sandbox" are different
// questions, and one must never be implemented in terms of the other.
//
// It asserts the shape rather than one provider's current value, so it keeps
// working as providers move across either axis -- which they do: exe was
// sandbox-placed-but-not-selectable until its boundaries were signed off.
func TestRuntimeProviderAxesAreIndependent(t *testing.T) {
	// local is the standing witness: selectable by every caller, and its leads
	// are plain local processes.
	if !ClientSelectableRuntimeProvider(RuntimeProviderLocal) {
		t.Error("local must stay client-selectable")
	}
	if SandboxPlacedRuntimeProvider(RuntimeProviderLocal) {
		t.Error("local leads are not sandbox-placed")
	}

	differs := false
	for provider := range selectableByDesign {
		if ClientSelectableRuntimeProvider(provider) != SandboxPlacedRuntimeProvider(provider) {
			differs = true
			break
		}
	}
	if !differs {
		t.Error("the two axes agree on every provider; one is being derived from the other")
	}
}

// TestExeIsSandboxPlaced pins the half of exe's classification that does not
// depend on a review decision: wherever it is selectable or not, its leads run
// in a remote VM and must be provisioned, attached and reaped as such.
func TestExeIsSandboxPlaced(t *testing.T) {
	if !SandboxPlacedRuntimeProvider(RuntimeProviderExe) {
		t.Error("exe leads are placed in a sandbox")
	}
}

func TestClientSelectableRuntimeProviderMatchesDesign(t *testing.T) {
	for provider, want := range selectableByDesign {
		if got := ClientSelectableRuntimeProvider(provider); got != want {
			t.Errorf("ClientSelectableRuntimeProvider(%q) = %v, want %v", provider, got, want)
		}
	}
}

func TestClientSelectableRuntimeProviderFailsClosed(t *testing.T) {
	for _, p := range []RuntimeProvider{
		"",
		"exe ",
		"EXE",
		"Daytona",
		"daytona\n",
		"unknown-provider",
		"../../etc/passwd",
	} {
		if ClientSelectableRuntimeProvider(p) {
			t.Errorf("ClientSelectableRuntimeProvider(%q) = true, want false (must fail closed)", p)
		}
	}
}

// TestEveryRuntimeProviderHasADeclaredVerdict is the guard that makes the
// boundary hard to widen by accident. Adding a RuntimeProvider constant is
// routine; quietly making it caller-selectable is not. Parsing the enum out of
// the source means a new constant fails HERE, with an explanation, instead of
// being silently absent from the boundary's test coverage.
func TestEveryRuntimeProviderHasADeclaredVerdict(t *testing.T) {
	declared := runtimeProviderConstants(t)
	if len(declared) == 0 {
		t.Fatal("parsed zero RuntimeProvider constants; the parser below is broken, not the enum")
	}
	for _, provider := range declared {
		if _, ok := sandboxPlacedByDesign[provider]; !ok {
			t.Errorf("RuntimeProvider %q has no entry in sandboxPlacedByDesign; "+
				"decide whether its leads run in a remote sandbox or as a local process", provider)
		}
		if _, ok := selectableByDesign[provider]; !ok {
			t.Errorf(`RuntimeProvider %q has no entry in selectableByDesign.

A new runtime provider must be an explicit decision, not an omission. Add it to
selectableByDesign, and to domain.ClientSelectableRuntimeProvider only if
letting CALLERS provision on it has been reviewed: a client-supplied
runtime_provider reaches the placement broker, and neither the OpenAPI schema
nor the UI constrains it.`, provider)
		}
	}
	for provider := range selectableByDesign {
		if !containsProvider(declared, provider) {
			t.Errorf("selectableByDesign lists %q, which is no longer a declared RuntimeProvider constant", provider)
		}
	}
}

func containsProvider(list []RuntimeProvider, want RuntimeProvider) bool {
	for _, p := range list {
		if p == want {
			return true
		}
	}
	return false
}

// runtimeProviderConstants reads the RuntimeProvider const values out of
// control_plane.go. Go has no runtime enumeration of constants, so the source
// is the only place the full set exists.
func runtimeProviderConstants(t *testing.T) []RuntimeProvider {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "control_plane.go", nil, 0)
	if err != nil {
		t.Fatalf("parse control_plane.go: %v", err)
	}
	var out []RuntimeProvider
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || !isRuntimeProviderType(value.Type) {
				continue
			}
			for _, v := range value.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				unquoted, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", lit.Value, err)
				}
				out = append(out, RuntimeProvider(unquoted))
			}
		}
	}
	return out
}

func isRuntimeProviderType(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "RuntimeProvider"
}
