package workflow

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestSpecifierAllowed(t *testing.T) {
	allowed := []string{
		"./x", "../x", "/abs", "#internal", "data:text/javascript,export%20default%201",
		"node:fs", "node:module", "fs", "fs/promises", "path", "@loom/sdk", "@loom/sdk/driver", "",
	}
	for _, spec := range allowed {
		if !specifierAllowed(spec) {
			t.Errorf("specifierAllowed(%q) = false, want true", spec)
		}
	}
	rejected := []string{
		"@daytona/sdk", "@flue/runtime", "@flue/runtime/internal", "hono", "@hono/node-server",
		"node-liblzma", "@mongodb-js/zstd", "lodash", "@loom/sdk-extras", "@loom/other",
	}
	for _, spec := range rejected {
		if specifierAllowed(spec) {
			t.Errorf("specifierAllowed(%q) = true, want false", spec)
		}
	}
}

// literalAt reports whether the first occurrence of needle in src sits inside
// a literal/comment region.
func literalAt(t *testing.T, src, needle string) bool {
	t.Helper()
	offset := strings.Index(src, needle)
	if offset < 0 {
		t.Fatalf("needle %q not found in %q", needle, src)
	}
	return scanLiteralRegions([]byte(src)).contains(offset)
}

func TestScanLiteralRegionsStrings(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		literal []string // needles expected inside a literal
		code    []string // needles expected in code
	}{
		{"single quotes", "const a = 'require(\"x\")'; require('y');", []string{`require("x")`}, []string{"require('y')"}},
		{"double quotes", "const a = \"import 'x'\"; import z from 'z';", []string{"import 'x'"}, []string{"import z"}},
		{"template", "const a = `require('x')`; const b = require('y');", []string{"require('x')"}, []string{"require('y')"}},
		{"escaped double quote", "const a = \"say \\\"hi\\\" require('x')\"; require('y');", []string{"require('x')"}, []string{"require('y')"}},
		{"escaped single quote", "const a = 'it\\'s require(\"x\")'; require('y');", []string{`require("x")`}, []string{"require('y')"}},
		{"unterminated string resyncs at newline", "const a = 'oops\nrequire('y');", []string{"oops"}, []string{"require('y')"}},
		{"unterminated template runs to end", "const a = `open\nrequire('y');", []string{"open", "require('y')"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, needle := range tc.literal {
				if !literalAt(t, tc.src, needle) {
					t.Errorf("%q should be inside a literal in %q", needle, tc.src)
				}
			}
			for _, needle := range tc.code {
				if literalAt(t, tc.src, needle) {
					t.Errorf("%q should be code in %q", needle, tc.src)
				}
			}
		})
	}
}

func TestScanLiteralRegionsComments(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		literal []string
		code    []string
	}{
		{"line comment", "// require('x')\nrequire('y');", []string{"require('x')"}, []string{"require('y')"}},
		{"trailing line comment", "require('y'); // require('x')", []string{"require('x')"}, []string{"require('y')"}},
		{"block comment", "/* require('x') */ require('y');", []string{"require('x')"}, []string{"require('y')"}},
		{"multi-line block comment", "/**\n * @example require('x')\n */\nrequire('y');", []string{"require('x')"}, []string{"require('y')"}},
		{"quote inside comment does not open a string", "// it's\nrequire('y');", []string{"it's"}, []string{"require('y')"}},
		{"comment marker inside string is not a comment", "const u = 'http://h/require(\"x\")'; require('y');", []string{`require("x")`}, []string{"require('y')"}},
		{"unterminated block comment runs to end", "/* open\nrequire('y');", []string{"open", "require('y')"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, needle := range tc.literal {
				if !literalAt(t, tc.src, needle) {
					t.Errorf("%q should be inside a literal in %q", needle, tc.src)
				}
			}
			for _, needle := range tc.code {
				if literalAt(t, tc.src, needle) {
					t.Errorf("%q should be code in %q", needle, tc.src)
				}
			}
		})
	}
}

func TestLiteralRegionsContains(t *testing.T) {
	regions := literalRegions{{2, 5}, {10, 12}}
	cases := map[int]bool{0: false, 1: false, 2: true, 4: true, 5: false, 9: false, 10: true, 11: true, 12: false, 100: false}
	for offset, want := range cases {
		if got := regions.contains(offset); got != want {
			t.Errorf("contains(%d) = %t, want %t", offset, got, want)
		}
	}
	if (literalRegions{}).contains(0) {
		t.Errorf("empty regions should contain nothing")
	}
}

func TestDlopenReferenced(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"call in code", "process.dlopen(module, filename);", true},
		{"member access in code", "const f = process.dlopen;", true},
		{"mention inside string", "const reason = \"process.dlopen allows loading native addons\";", false},
		{"mention inside template", "const reason = `process.dlopen allows loading native addons`;", false},
		{"mention in line comment", "// process.dlopen is blocked by the sandbox\nexport {};", false},
		{"mention in block comment", "/* process.dlopen is blocked */ export {};", false},
		{"call-shaped inside string is always flagged", "const s = \"process.dlopen(x)\";", true},
		{"call-shaped with whitespace inside string", "const s = 'process.dlopen  (x)';", true},
		{"bracket-shaped inside string is flagged", "const s = \"process.dlopen[0]\";", true},
		{"unrelated identifier", "const dlopenish = process.dlopenExtra;", false},
		{"absent", "export {};", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.src)
			if got := dlopenReferenced(data, scanLiteralRegions(data)); got != tc.want {
				t.Fatalf("dlopenReferenced(%q) = %t, want %t", tc.src, got, tc.want)
			}
		})
	}
}

func TestScanLiteralRegionsRegexAndTemplates(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		literal []string
		code    []string
	}{
		{"regex quote does not open a string", "const re=/\"/;import x from \"hono\";", []string{`/"/`}, []string{"from"}},
		{"regex slash-star is not a comment", "const m = s.match(/\\/*$/)[1];\nrequire(\"@daytona/sdk\");", []string{"/\\/*$/"}, []string{"require"}},
		{"regex backtick does not open a template", "const re=/`/;\nrequire('y');", []string{"/`/"}, []string{"require"}},
		{"regex class may hold a slash", "const re=/[/]x/;require('y');", []string{"[/]x"}, []string{"require"}},
		{"division is code", "const a = b / c / d;\nrequire('y');", nil, []string{"b / c", "require"}},
		{"regex after return", "function f(){ return /\"/; }\nrequire('y');", []string{`/"/`}, []string{"require"}},
		{"unterminated regex resyncs at newline", "const a = /oops\nrequire('y');", []string{"oops"}, []string{"require"}},
		{"nested template substitution", "const t = `${p1 ? `[^/]+` : \"[^/]*\"}(?=$|\\\\/$)`;\nrequire('y');", []string{"[^/]+", "(?=$"}, []string{"p1 ?", "require"}},
		{"code inside substitution", "const t = `a ${__require(\"hono\")} b`;", []string{"a $", " b"}, []string{"__require"}},
		{"braces inside substitution", "const t = `${ {a:1}.a } b`;require('y');", []string{" b"}, []string{"{a:1}", "require"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, needle := range tc.literal {
				if !literalAt(t, tc.src, needle) {
					t.Errorf("%q should be inside a literal in %q", needle, tc.src)
				}
			}
			for _, needle := range tc.code {
				if literalAt(t, tc.src, needle) {
					t.Errorf("%q should be code in %q", needle, tc.src)
				}
			}
		})
	}
}

// Rule (iii) must see every shape a forbidden specifier takes in bundler
// output, and a scanner misparse must never hide a static import.
func TestAuditBareSpecifierShapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		bare []string
		dyn  []string
	}{
		{"createRequire shim", `var h = __require("hono");`, []string{"hono"}, nil},
		{"require after dot is not a require call", `const x = mod.require("hono");`, nil, nil},
		{"side-effect import after statement", `export {};import "hono";`, []string{"hono"}, nil},
		{"import after regex with quote", "const re=/\"/;import x from \"hono\";", []string{"hono"}, nil},
		{"top-level import survives any misparse", "const s = 'oops\nimport x from \"hono\";", []string{"hono"}, nil},
		{"export from", `export { a } from "@daytona/sdk";`, []string{"@daytona/sdk"}, nil},
		{"string mention is not an import", `const s = "import x from \"hono\"";`, nil, nil},
		{"comment mention is not an import", "// import x from \"hono\"\n/* require(\"hono\") */", nil, nil},
		{"codegen template is not an import", "const t = `import x from \"hono\";\nrequire(\"hono\")`;", nil, nil},
		{"dynamic import is reported", `const m = await import("node-liblzma");`, nil, []string{"node-liblzma"}},
		{"allowed specifiers", "import fs from \"node:fs\";import p from \"path\";import { x } from \"@loom/sdk\";import y from \"./y.js\";", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &artifactAuditor{bare: map[string]struct{}{}, dyn: map[string]struct{}{}}
			data := []byte(tc.src)
			literals := scanLiteralRegions(data)
			for _, re := range []*regexp.Regexp{reImportFrom, reSideEffect, reRequire} {
				collectDisallowed(re, data, literals, a.bare)
			}
			collectDisallowed(reTopLevelImport, data, nil, a.bare)
			collectDisallowed(reDynamicImport, data, literals, a.dyn)
			if got := sortedKeys(a.bare); !reflect.DeepEqual(got, tc.bare) && !(len(got) == 0 && len(tc.bare) == 0) {
				t.Errorf("bare = %v, want %v", got, tc.bare)
			}
			if got := sortedKeys(a.dyn); !reflect.DeepEqual(got, tc.dyn) && !(len(got) == 0 && len(tc.dyn) == 0) {
				t.Errorf("dyn = %v, want %v", got, tc.dyn)
			}
		})
	}
}
