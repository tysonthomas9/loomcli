package workflow

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// auditReport is the static audit result for a staged packaged dist
// (DEV-V5-31 rules i–vi). It is reported in package-builtin's output.
type auditReport struct {
	NativeFiles    []string `json:"native_files"`
	BareSpecifiers []string `json:"bare_specifiers"`
	// DynamicBareSpecifiers are bare specifiers seen only in dynamic
	// `import("…")` calls. They are REPORTED, not failed: a deferred import
	// cannot break module evaluation, and bundled dependencies use exactly
	// this shape for optional backends (e.g. `import("node-liblzma")` /
	// `import("@mongodb-js/zstd")` behind try/catch for xz/zstd compression).
	// The artifact ships no node_modules for them, so they fail at call time
	// inside the library's own guard.
	DynamicBareSpecifiers []string `json:"dynamic_bare_specifiers"`
	Symlinks              []string `json:"symlinks"`
	Dlopen                bool     `json:"dlopen"`
	CreateRequireCount    int      `json:"create_require_count"`
}

var (
	reImportFrom = regexp.MustCompile(`\b(from)\s*["']([^"'\s]+)["']`)
	// Side-effect imports may follow a statement on the same line
	// (`export {};import "x"`), so anchor on statement boundaries, not lines.
	reSideEffect = regexp.MustCompile(`(?:^|[;{}\n])\s*(import)\s*["']([^"'\s]+)["']`)
	// `__require("x")` is the createRequire shim bundlers emit for a CJS
	// require of an external inside ESM output — the primary shape a
	// forbidden CJS external takes in a Flue-built dist, so no \b before it.
	reRequire       = regexp.MustCompile(`(?:^|[^\w$.])((?:__)?require)\(\s*["']([^"'\s]+)["']\s*\)`)
	reDynamicImport = regexp.MustCompile(`\b(import)\(\s*["']([^"'\s]+)["']\s*\)`)
	reDlopen        = regexp.MustCompile(`\bprocess\.dlopen\b`)
	reCreateRequire = regexp.MustCompile(`\bcreateRequire\b`)
	// reTopLevelImport is applied to the UNFILTERED bytes: bundlers only emit
	// static import/export-from at line start, so a literal-scanner misparse
	// (a quote inside a regex the scanner did not model) can never hide one.
	reTopLevelImport = regexp.MustCompile(`(?m)^[ \t]*(import|export)\b[^\n;]*?\bfrom\s*["']([^"'\s]+)["']`)
)

// nodeBuiltinModules is the static allowlist of bare Node built-in names
// (base segment before the first "/").
var nodeBuiltinModules = map[string]struct{}{
	"assert": {}, "async_hooks": {}, "buffer": {}, "child_process": {}, "cluster": {}, "console": {},
	"constants": {}, "crypto": {}, "dgram": {}, "diagnostics_channel": {}, "dns": {}, "domain": {},
	"events": {}, "fs": {}, "http": {}, "http2": {}, "https": {}, "inspector": {}, "module": {},
	"net": {}, "os": {}, "path": {}, "perf_hooks": {}, "process": {}, "punycode": {}, "querystring": {},
	"readline": {}, "repl": {}, "sea": {}, "sqlite": {}, "stream": {}, "string_decoder": {}, "sys": {},
	"test": {}, "timers": {}, "tls": {}, "trace_events": {}, "tty": {}, "url": {}, "util": {}, "v8": {},
	"vm": {}, "wasi": {}, "worker_threads": {}, "zlib": {},
}

// specifierAllowed reports whether a static import specifier may appear in a
// packaged dist: relative/absolute/subpath-import/data: specifiers, node:*,
// Node built-ins, and the nested @loom/sdk runtime. Every other bare
// specifier (other scopes, hono, @daytona/sdk, @flue/runtime, …) would need
// a node_modules the artifact does not ship.
func specifierAllowed(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return true
	}
	switch {
	case strings.HasPrefix(spec, "."), strings.HasPrefix(spec, "/"), strings.HasPrefix(spec, "#"), strings.HasPrefix(spec, "data:"):
		return true
	case strings.HasPrefix(spec, "node:"):
		return true
	case spec == "@loom/sdk" || strings.HasPrefix(spec, "@loom/sdk/"):
		return true
	case strings.HasPrefix(spec, "@"):
		return false
	}
	base, _, _ := strings.Cut(spec, "/")
	_, ok := nodeBuiltinModules[base]
	return ok
}

func isScriptFile(name string) bool {
	switch filepath.Ext(name) {
	case ".mjs", ".js":
		return true
	}
	return false
}

// auditArtifact walks a staged dist and applies rules i–vi. The first failing
// rule decides the error, but the report lists every offender so the message
// is actionable. Rules iii/iv apply outside node_modules/ only; i, ii, v apply
// everywhere under dist. Matches inside string/template literals or comments
// never count (a 10 MB bundle is full of JSDoc `require('x')` examples and
// error strings naming packages).
func auditArtifact(dist string) (auditReport, error) {
	a := &artifactAuditor{
		report: auditReport{NativeFiles: []string{}, BareSpecifiers: []string{}, DynamicBareSpecifiers: []string{}, Symlinks: []string{}},
		bare:   map[string]struct{}{},
		dyn:    map[string]struct{}{},
	}
	err := filepath.WalkDir(dist, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(path, dist), string(filepath.Separator)))
		return a.visit(path, rel, entry)
	})
	if err != nil {
		return a.report, fmt.Errorf("builtin_artifact_invalid: artifact audit: walk %s: %w", dist, err)
	}
	a.report.BareSpecifiers = sortedKeys(a.bare)
	a.report.DynamicBareSpecifiers = sortedKeys(a.dyn)
	sort.Strings(a.report.NativeFiles)
	sort.Strings(a.report.Symlinks)
	sdkMissing := false
	if info, err := os.Stat(filepath.Join(dist, "node_modules", "@loom", "sdk", "package.json")); err != nil || !info.Mode().IsRegular() {
		sdkMissing = true
	}
	return a.report, auditVerdict(a.report, sdkMissing)
}

type artifactAuditor struct {
	report auditReport
	bare   map[string]struct{}
	dyn    map[string]struct{}
}

// visit applies the per-entry rules: symlinks (v) and native files (i) for
// every entry, dlopen (ii) for every script, and specifier rules (iii/iv) for
// scripts outside node_modules/.
func (a *artifactAuditor) visit(path, rel string, entry fs.DirEntry) error {
	if entry.Type()&fs.ModeSymlink != 0 {
		a.report.Symlinks = append(a.report.Symlinks, rel)
		return nil
	}
	if entry.IsDir() {
		return nil
	}
	if filepath.Ext(entry.Name()) == ".node" {
		a.report.NativeFiles = append(a.report.NativeFiles, rel)
	}
	if !isScriptFile(entry.Name()) {
		return nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // path comes from walking the staged dist.
	if err != nil {
		return err
	}
	literals := scanLiteralRegions(data)
	if dlopenReferenced(data, literals) {
		a.report.Dlopen = true
	}
	if strings.HasPrefix(rel, "node_modules/") || strings.Contains(rel, "/node_modules/") {
		return nil
	}
	a.report.CreateRequireCount += len(reCreateRequire.FindAllIndex(data, -1))
	for _, re := range []*regexp.Regexp{reImportFrom, reSideEffect, reRequire} {
		collectDisallowed(re, data, literals, a.bare)
	}
	collectDisallowed(reTopLevelImport, data, nil, a.bare)
	collectDisallowed(reDynamicImport, data, literals, a.dyn)
	return nil
}

// collectDisallowed records every specifier matched by re that
// specifierAllowed rejects. Every specifier regex captures the keyword
// (group 1) and the specifier (group 2); the keyword position decides whether
// the match is code or sits inside a literal/comment region (the specifier
// itself is always inside a string). A nil literals applies re to every byte.
func collectDisallowed(re *regexp.Regexp, data []byte, literals literalRegions, into map[string]struct{}) {
	for _, match := range re.FindAllSubmatchIndex(data, -1) {
		if literals != nil && literals.contains(match[2]) {
			continue // JSDoc examples, error strings, codegen templates
		}
		if spec := string(data[match[4]:match[5]]); !specifierAllowed(spec) {
			into[spec] = struct{}{}
		}
	}
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// auditVerdict turns the report into the first failing rule's error.
func auditVerdict(report auditReport, sdkMissing bool) error {
	switch {
	case len(report.NativeFiles) > 0:
		return fmt.Errorf("builtin_artifact_invalid: artifact audit: native_files %s", strings.Join(report.NativeFiles, ","))
	case report.Dlopen:
		return fmt.Errorf("builtin_artifact_invalid: artifact audit: dlopen process.dlopen referenced")
	case len(report.BareSpecifiers) > 0:
		return fmt.Errorf("builtin_artifact_invalid: artifact audit: bare_specifiers %s", strings.Join(report.BareSpecifiers, ","))
	case len(report.Symlinks) > 0:
		return fmt.Errorf("builtin_artifact_invalid: artifact audit: symlinks %s", strings.Join(report.Symlinks, ","))
	case sdkMissing:
		return fmt.Errorf("builtin_artifact_invalid: artifact audit: loom_sdk dist/node_modules/@loom/sdk/package.json missing")
	}
	return nil
}

// dlopenReferenced implements rule (ii) by intent: a `process.dlopen` CODE
// reference fails the audit, while a mention inside a string literal or
// comment does not — the Flue runtime ships a sandbox denylist whose reason
// text reads "process.dlopen allows loading native addons", and that table
// is what BLOCKS native loading. Call-shaped occurrences (`process.dlopen(`,
// `.call`, `[`) are flagged regardless of the scanner's verdict so a
// misparsed regex literal can only produce a loud false positive, never a
// silent miss.
func dlopenReferenced(data []byte, literals literalRegions) bool {
	for _, loc := range reDlopen.FindAllIndex(data, -1) {
		if callShaped(data, loc[1]) || !literals.contains(loc[0]) {
			return true
		}
	}
	return false
}

func callShaped(data []byte, end int) bool {
	for i := end; i < len(data); i++ {
		switch data[i] {
		case ' ', '\t', '\r', '\n':
			continue
		case '(', '.', '[':
			return true
		}
		return false
	}
	return false
}

// literalRegions are the sorted, non-overlapping [start,end) byte ranges of
// a script that sit inside string/template literals or comments.
type literalRegions [][2]int

func (r literalRegions) contains(offset int) bool {
	i := sort.Search(len(r), func(i int) bool { return r[i][1] > offset })
	return i < len(r) && r[i][0] <= offset
}

// scanLiteralRegions runs a single pass over data tracking JS string,
// template (including `${…}` substitutions, which re-enter code), comment,
// and regex-literal state. Regex detection is heuristic (a `/` after an
// operator, opener, or expression keyword starts one) and every non-template
// state resyncs at end of line, so a misparse is confined to one line. The
// rules that matter for safety do not rely on it alone: dlopen call shapes
// are flagged regardless of region, and static import/export-from lines are
// matched on the unfiltered bytes (reTopLevelImport), so a misparse can make
// the audit noisier, never silently permissive.
func scanLiteralRegions(data []byte) literalRegions {
	sc := &literalScanner{data: data}
	for i := 0; i < len(data); i++ {
		i = sc.step(i)
	}
	if sc.state != literalCode {
		sc.regions = append(sc.regions, [2]int{sc.start, len(data)})
	}
	return sc.regions
}

type literalScanner struct {
	data    []byte
	regions literalRegions
	state   int
	start   int
	// subst holds the `{` depth of each open template substitution, innermost
	// last; a `}` at depth 0 returns to the enclosing template.
	subst []int
}

// step consumes data[i] in the current state and returns the index of the
// last byte consumed.
func (sc *literalScanner) step(i int) int {
	c := sc.data[i]
	switch sc.state {
	case literalCode:
		return sc.stepCode(i)
	case literalTemplate:
		if c == '$' && i+1 < len(sc.data) && sc.data[i+1] == '{' {
			sc.close(i + 2)
			sc.subst = append(sc.subst, 0)
			return i + 1
		}
		fallthrough
	case literalSingle, literalDouble, literalRegex:
		if c == '\\' {
			return i + 1
		}
		if sc.state == literalRegex && c == '[' {
			return regexClassEnd(sc.data, i)
		}
		if literalCloses(sc.state, c) {
			sc.close(i + 1)
		}
	case literalLineComment:
		if c == '\n' {
			sc.close(i)
		}
	case literalBlockComment:
		if c == '*' && i+1 < len(sc.data) && sc.data[i+1] == '/' {
			sc.close(i + 2)
			return i + 1
		}
	}
	return i
}

func (sc *literalScanner) stepCode(i int) int {
	if n := len(sc.subst); n > 0 {
		switch sc.data[i] {
		case '{':
			sc.subst[n-1]++
		case '}':
			if sc.subst[n-1] == 0 {
				sc.subst = sc.subst[:n-1]
				sc.state, sc.start = literalTemplate, i
				return i
			}
			sc.subst[n-1]--
		}
	}
	state, skip := literalOpens(sc.data, i)
	if state != literalCode {
		sc.state, sc.start = state, i
	}
	return i + skip
}

func (sc *literalScanner) close(end int) {
	sc.regions = append(sc.regions, [2]int{sc.start, end})
	sc.state = literalCode
}

const (
	literalCode = iota
	literalSingle
	literalDouble
	literalTemplate
	literalLineComment
	literalBlockComment
	literalRegex
)

// literalOpens reports the literal state data[i] opens (literalCode when it
// opens none) and how many extra bytes the opener consumed.
func literalOpens(data []byte, i int) (state, skip int) {
	switch c := data[i]; {
	case c == '\'':
		return literalSingle, 0
	case c == '"':
		return literalDouble, 0
	case c == '`':
		return literalTemplate, 0
	case c == '/' && i+1 < len(data) && data[i+1] == '/':
		return literalLineComment, 0
	case c == '/' && i+1 < len(data) && data[i+1] == '*':
		return literalBlockComment, 1
	case c == '/' && regexStartsAt(data, i):
		return literalRegex, 0
	}
	return literalCode, 0
}

// regexKeywords are the tokens after which a `/` begins a regex literal
// rather than a division.
var regexKeywords = map[string]struct{}{
	"return": {}, "typeof": {}, "case": {}, "do": {}, "else": {}, "in": {}, "of": {},
	"instanceof": {}, "new": {}, "delete": {}, "void": {}, "throw": {}, "yield": {}, "await": {},
}

// regexStartsAt reports whether the `/` at data[i] starts a regex literal:
// true at start of input or after an operator/opener/keyword, false after a
// value (identifier, number, `)`, `]`) where it is a division.
func regexStartsAt(data []byte, i int) bool {
	j := i - 1
	for j >= 0 && (data[j] == ' ' || data[j] == '\t') {
		j--
	}
	if j < 0 || data[j] == '\n' || data[j] == '\r' {
		return true
	}
	if strings.IndexByte("(,=:[!&|?{};+-*%<>~^", data[j]) >= 0 {
		return true
	}
	end := j + 1
	for j >= 0 && isIdentByte(data[j]) {
		j--
	}
	_, keyword := regexKeywords[string(data[j+1:end])]
	return keyword
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// regexClassEnd returns the index of the `]` closing the character class
// opened at data[i] (or the end of line / input when unterminated).
func regexClassEnd(data []byte, i int) int {
	for k := i + 1; k < len(data); k++ {
		switch data[k] {
		case '\\':
			k++
		case ']':
			return k
		case '\n':
			return k - 1
		}
	}
	return len(data) - 1
}

// literalCloses reports whether c ends the string/regex state. An
// unterminated single/double-quoted string or regex resyncs at end of line.
func literalCloses(state int, c byte) bool {
	switch state {
	case literalSingle:
		return c == '\'' || c == '\n'
	case literalDouble:
		return c == '"' || c == '\n'
	case literalTemplate:
		return c == '`'
	case literalRegex:
		return c == '/' || c == '\n'
	}
	return false
}
