package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"golang.org/x/tools/go/packages"
	"gopkg.in/yaml.v3"
)

// golangciConfigFile is the checked-in linter config that encodes the enforced
// package-layer boundaries under linters.settings.depguard.rules.
const golangciConfigFile = ".golangci.yml"

// depguardConfig is the subset of .golangci.yml the layers generator reads: the
// depguard rules that define, and enforce, the module's layering.
type depguardConfig struct {
	Linters struct {
		Settings struct {
			Depguard struct {
				Rules map[string]depguardRule `yaml:"rules"`
			} `yaml:"depguard"`
		} `yaml:"settings"`
		// Exclusions.Paths removes matching files from ALL linters, depguard
		// included — so a rule glob can "match" a package that depguard never
		// actually checks. Captured to disclose that gap.
		Exclusions struct {
			Paths []string `yaml:"paths"`
		} `yaml:"exclusions"`
	} `yaml:"linters"`
}

// depguardRule is one named boundary: the files it governs (globs), the imports
// it forbids, and — in strict mode only — the imports it exclusively allows.
type depguardRule struct {
	ListMode string         `yaml:"list-mode"`
	Files    []string       `yaml:"files"`
	Deny     []depguardDeny `yaml:"deny"`
	Allow    []string       `yaml:"allow"`
}

// depguardDeny is a single forbidden import with its human-written reason.
type depguardDeny struct {
	Pkg  string `yaml:"pkg"`
	Desc string `yaml:"desc"`
}

// generateLayers renders the package-layer architecture reference body from two
// inputs: the depguard rules in .golangci.yml (the intended, enforced layering)
// and the module's real import graph from cfg.Packages() (the actual structure).
// The value is turning architecture that otherwise lives only in a linter config
// into a human-readable doc that cannot drift from what the build enforces.
func generateLayers(cfg *genConfig) (string, error) {
	raw, err := os.ReadFile(filepath.Join(cfg.RepoRoot, golangciConfigFile)) //nolint:gosec // G304: fixed repo-relative config filename
	if err != nil {
		// A missing config is not a hard error: the generator degrades to a
		// deterministic note so a bare tree (e.g. the determinism test's temp
		// root) still renders. The real repo always ships the config.
		if errors.Is(err, fs.ErrNotExist) {
			return layersMissingConfigBody(), nil
		}
		return "", fmt.Errorf("read %s: %w", golangciConfigFile, err)
	}
	var parsed depguardConfig
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("parse %s: %w", golangciConfigFile, err)
	}
	pkgs, err := cfg.Packages()
	if err != nil {
		return "", err
	}
	model := buildLayerModel(cfg, parsed, pkgs, string(raw))
	return renderLayerDoc(model), nil
}

// layerModel is the fully-computed, pre-sorted view the renderer consumes. Every
// slice is sorted at construction so rendering is a straight walk — no map is
// ranged for output order anywhere downstream.
type layerModel struct {
	modulePath     string
	direction      string           // e.g. "sdk → infra → web → cli", parsed from a config comment
	externalRefs   []string         // absolute-path config references (a documented gap)
	rules          []layerRuleView  // sorted by rule name
	crossImports   []layerCross     // sorted by rule name
	violations     []layerViolation // forbidden edges actually present (expected empty)
	deadGlobs      []layerGlob      // file globs that match no tracked package
	staleDenies    []layerStaleDeny // deny targets with no tracked package
	excludedPaths  []string         // linters.exclusions.paths patterns (raw)
	excludedPkgs   []string         // governed internal packages that lint excludes
	ungovernedPkgs []string         // internal packages no rule's globs match
	governedPkgs   int              // unique first-party internal packages under a rule
	totalPkgs      int              // total first-party internal packages loaded
}

// layerRuleView is one depguard rule with the real packages it governs.
type layerRuleView struct {
	name     string
	listMode string
	files    []string
	denies   []depguardDeny
	allow    []string
	packages []layerPkg // sorted by path
}

// layerPkg pairs a package path with its purpose, taken from the package's doc
// comment (never invented).
type layerPkg struct {
	path    string
	purpose string
}

// layerCross summarizes, for one rule, the other rules whose packages its own
// packages actually import.
type layerCross struct {
	name    string
	targets []string
}

// layerViolation is a real import edge that a governing rule forbids. depguard
// already gates these in CI, so this is a light cross-check, expected empty.
type layerViolation struct {
	from string
	imp  string
	rule string
	desc string
}

// layerGlob is a rule's file glob that matched no tracked package.
type layerGlob struct {
	rule string
	glob string
}

// layerStaleDeny is a denied import path that no tracked package provides.
type layerStaleDeny struct {
	pkg   string
	rules []string
}

// buildLayerModel computes the whole model deterministically from the parsed
// config and the loaded package graph.
func buildLayerModel(cfg *genConfig, parsed depguardConfig, pkgs []*packages.Package, raw string) layerModel {
	rules := parsed.Linters.Settings.Depguard.Rules
	modPath := layerModulePath(pkgs)
	relFiles := map[string][]string{}
	imports := map[string][]string{}
	byRel := map[string]*packages.Package{}
	present := map[string]bool{}
	internal := 0
	for _, p := range pkgs {
		rel := layerRelPath(p, modPath)
		byRel[rel] = p
		present[p.PkgPath] = true
		relFiles[rel] = layerRepoRelFiles(cfg.RepoRoot, p)
		imports[rel] = layerInternalImports(p, modPath)
		if strings.HasPrefix(rel, "internal/") {
			internal++
		}
	}
	pkgRules, rulePkgs := layerGovernance(rules, relFiles)
	exPaths := parsed.Linters.Exclusions.Paths
	return layerModel{
		modulePath:     modPath,
		direction:      layerDirection(raw),
		externalRefs:   layerExternalRefs(raw),
		rules:          layerRuleViews(rules, rulePkgs, byRel),
		crossImports:   layerCrossImports(rules, rulePkgs, pkgRules, imports),
		violations:     layerViolations(rules, rulePkgs, imports, modPath),
		deadGlobs:      layerDeadGlobs(rules, relFiles),
		staleDenies:    layerStaleDenies(rules, present),
		excludedPaths:  layerSortedCopy(exPaths),
		excludedPkgs:   layerExcludedGoverned(exPaths, relFiles, pkgRules),
		ungovernedPkgs: layerUngoverned(relFiles, pkgRules),
		governedPkgs:   len(pkgRules),
		totalPkgs:      internal,
	}
}

// layerUngoverned returns the first-party internal/ packages that no depguard
// rule's file globs match, so depguard checks none of their imports. The
// overview reports this as a count; naming them is what makes it actionable,
// since a reader otherwise cannot tell whether their own package is covered.
// Sorted.
func layerUngoverned(relFiles map[string][]string, pkgRules map[string][]string) []string {
	var out []string
	for rel := range relFiles {
		if !strings.HasPrefix(rel, "internal/") {
			continue
		}
		if len(pkgRules[rel]) == 0 {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// layerExcludedGoverned returns the governed internal packages whose files match
// a linters.exclusions.paths pattern, i.e. packages a rule's globs match but
// that depguard never actually checks. Sorted, deduped.
func layerExcludedGoverned(patterns []string, relFiles map[string][]string, pkgRules map[string][]string) []string {
	if len(patterns) == 0 {
		return nil
	}
	res := layerCompilePatterns(patterns)
	var out []string
	for rel := range pkgRules { // only packages some rule governs
		if layerAnyFileExcluded(relFiles[rel], res) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// layerCompilePatterns compiles exclusion patterns as regexps, matching how
// golangci-lint treats linters.exclusions.paths. An uncompilable pattern is
// skipped (golangci would not honor it either), never treated as match-all.
func layerCompilePatterns(patterns []string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, p := range patterns {
		if re, err := regexp.Compile(p); err == nil {
			out = append(out, re)
		}
	}
	return out
}

func layerAnyFileExcluded(files []string, res []*regexp.Regexp) bool {
	for _, f := range files {
		for _, re := range res {
			if re.MatchString(f) {
				return true
			}
		}
	}
	return false
}

// layerModulePath returns the module import path (e.g.
// github.com/tysonthomas9/loomcli) from the first package that carries module
// info, falling back to the shared prefix of loaded package paths.
func layerModulePath(pkgs []*packages.Package) string {
	for _, p := range pkgs {
		if p.Module != nil && p.Module.Path != "" {
			return p.Module.Path
		}
	}
	for _, p := range pkgs {
		if i := strings.Index(p.PkgPath, "/internal/"); i >= 0 {
			return p.PkgPath[:i]
		}
	}
	return ""
}

// layerRelPath renders a package's import path relative to the module root, so
// output carries internal/... paths, never the module prefix or absolute paths.
func layerRelPath(p *packages.Package, modPath string) string {
	return strings.TrimPrefix(p.PkgPath, modPath+"/")
}

// layerRepoRelFiles returns a package's Go files as sorted repo-relative slash
// paths, matching the shape depguard globs are written against.
func layerRepoRelFiles(root string, p *packages.Package) []string {
	out := make([]string, 0, len(p.GoFiles))
	for _, f := range p.GoFiles {
		if rel, err := filepath.Rel(root, f); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	sort.Strings(out)
	return out
}

// layerInternalImports returns a package's first-party internal imports as
// sorted module-relative paths (internal/...). pkg.Imports is a map, so its keys
// are sorted before use.
func layerInternalImports(p *packages.Package, modPath string) []string {
	prefix := modPath + "/internal/"
	var out []string
	for _, k := range layerSortedImportKeys(p.Imports) {
		if strings.HasPrefix(k, prefix) {
			out = append(out, strings.TrimPrefix(k, modPath+"/"))
		}
	}
	return out
}

// layerGovernance matches every loaded package's files against each rule's globs
// and returns both directions of the membership relation, all lists sorted.
func layerGovernance(rules map[string]depguardRule, relFiles map[string][]string) (pkgRules, rulePkgs map[string][]string) {
	pkgRules = map[string][]string{}
	rulePkgs = map[string][]string{}
	ruleNames := layerSortedRuleNames(rules)
	pkgRels := make([]string, 0, len(relFiles))
	for rel := range relFiles {
		pkgRels = append(pkgRels, rel)
	}
	sort.Strings(pkgRels)
	for _, rel := range pkgRels {
		for _, rn := range ruleNames {
			if layerFilesMatch(relFiles[rel], rules[rn].Files) {
				pkgRules[rel] = append(pkgRules[rel], rn)
				rulePkgs[rn] = append(rulePkgs[rn], rel)
			}
		}
	}
	return pkgRules, rulePkgs
}

// layerFilesMatch reports whether any of a package's files matches any glob.
func layerFilesMatch(files, globs []string) bool {
	for _, g := range globs {
		for _, f := range files {
			if ok, err := doublestar.Match(g, f); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// layerRuleViews assembles the per-rule detail, attaching each governed
// package's doc-comment purpose.
func layerRuleViews(rules map[string]depguardRule, rulePkgs map[string][]string, byRel map[string]*packages.Package) []layerRuleView {
	var out []layerRuleView
	for _, name := range layerSortedRuleNames(rules) {
		r := rules[name]
		denies := append([]depguardDeny(nil), r.Deny...)
		sort.Slice(denies, func(i, j int) bool { return denies[i].Pkg < denies[j].Pkg })
		pkgs := make([]layerPkg, 0, len(rulePkgs[name]))
		for _, rel := range rulePkgs[name] { // already sorted
			pkgs = append(pkgs, layerPkg{path: rel, purpose: layerSynopsis(byRel[rel])})
		}
		out = append(out, layerRuleView{
			name:     name,
			listMode: r.ListMode,
			files:    append([]string(nil), r.Files...),
			denies:   denies,
			allow:    append([]string(nil), r.Allow...),
			packages: pkgs,
		})
	}
	return out
}

// layerCrossImports computes, per rule, the sorted set of other rules (plus a
// "(ungoverned)" bucket) whose packages the rule's own packages import.
func layerCrossImports(rules map[string]depguardRule, rulePkgs, pkgRules map[string][]string, imports map[string][]string) []layerCross {
	var out []layerCross
	for _, name := range layerSortedRuleNames(rules) {
		targets := map[string]bool{}
		for _, pkg := range rulePkgs[name] {
			for _, imp := range imports[pkg] {
				govs := pkgRules[imp]
				if len(govs) == 0 {
					targets["(ungoverned)"] = true
					continue
				}
				for _, g := range govs {
					if g != name {
						targets[g] = true
					}
				}
			}
		}
		out = append(out, layerCross{name: name, targets: layerSortedSet(targets)})
	}
	return out
}

// layerViolations flags real import edges that a governing rule forbids. Matching
// is prefix-based, mirroring depguard, so a deny of internal/rpc also catches
// internal/rpc/sub. golangci-lint enforces this already; the list is expected
// empty and exists only as a cross-check.
func layerViolations(rules map[string]depguardRule, rulePkgs map[string][]string, imports map[string][]string, modPath string) []layerViolation {
	var out []layerViolation
	for _, name := range layerSortedRuleNames(rules) {
		for _, pkg := range rulePkgs[name] {
			for _, imp := range imports[pkg] {
				full := modPath + "/" + imp
				for _, d := range rules[name].Deny {
					if full == d.Pkg || strings.HasPrefix(full, d.Pkg+"/") {
						out = append(out, layerViolation{from: pkg, imp: imp, rule: name, desc: d.Desc})
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].from != out[j].from {
			return out[i].from < out[j].from
		}
		return out[i].imp < out[j].imp
	})
	return out
}

// layerDeadGlobs reports file globs that match no tracked package — a rule that
// governs nothing (stale target, typo, or a not-yet-created package).
func layerDeadGlobs(rules map[string]depguardRule, relFiles map[string][]string) []layerGlob {
	var out []layerGlob
	for _, name := range layerSortedRuleNames(rules) {
		for _, g := range rules[name].Files { // config order is deterministic
			matched := false
			for _, files := range relFiles {
				if layerFilesMatch(files, []string{g}) {
					matched = true
					break
				}
			}
			if !matched {
				out = append(out, layerGlob{rule: name, glob: g})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].glob != out[j].glob {
			return out[i].glob < out[j].glob
		}
		return out[i].rule < out[j].rule
	})
	return out
}

// layerStaleDenies reports denied import paths that no tracked package provides
// (neither the path itself nor any sub-package), collapsing duplicates across
// rules into a single entry that names every referencing rule.
func layerStaleDenies(rules map[string]depguardRule, present map[string]bool) []layerStaleDeny {
	refs := map[string]map[string]bool{}
	for _, name := range layerSortedRuleNames(rules) {
		for _, d := range rules[name].Deny {
			if layerPackagePresent(d.Pkg, present) {
				continue
			}
			if refs[d.Pkg] == nil {
				refs[d.Pkg] = map[string]bool{}
			}
			refs[d.Pkg][name] = true
		}
	}
	var out []layerStaleDeny
	for _, pkg := range layerSortedKeysOfSets(refs) {
		out = append(out, layerStaleDeny{pkg: pkg, rules: layerSortedSet(refs[pkg])})
	}
	return out
}

// layerPackagePresent reports whether a tracked package provides pkgPath or a
// sub-package of it.
func layerPackagePresent(pkgPath string, present map[string]bool) bool {
	if present[pkgPath] {
		return true
	}
	for p := range present {
		if strings.HasPrefix(p, pkgPath+"/") {
			return true
		}
	}
	return false
}

// layerDirection extracts the human-declared dependency direction from the
// config's comment (e.g. "sdk → infra → web → cli"), or "" if absent.
func layerDirection(raw string) string {
	const marker = "dependency direction:"
	for _, ln := range strings.Split(raw, "\n") {
		if i := strings.Index(ln, marker); i >= 0 {
			return strings.TrimSpace(ln[i+len(marker):])
		}
	}
	return ""
}

// layerExternalPathRe captures an absolute (/…) or home-relative (~/…)
// filesystem path with a document/source extension. The leading boundary group
// (start, whitespace, quote, '(', or '=') ensures the path begins a token, so a
// relative fragment inside another word — e.g. the "/plan.go" in a comment that
// reads "task.go/plan.go" — is not mistaken for an out-of-tree reference. Glob
// patterns (which contain '*', excluded from the character class) and package
// import paths (github.com/…, no leading '/' or '~') never match.
var layerExternalPathRe = regexp.MustCompile(`(?m)(?:^|[\s"'(=])([~/][\w./~+-]*\.(?:md|ya?ml|go|txt|json))`)

// layerExternalRefs finds config references to files outside the repository
// tree — architecture defined where it cannot be version-controlled with the
// code it governs. Only rooted paths (absolute or home-relative) qualify.
func layerExternalRefs(raw string) []string {
	set := map[string]bool{}
	for _, m := range layerExternalPathRe.FindAllStringSubmatch(raw, -1) {
		p := m[1]
		if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") {
			set[p] = true
		}
	}
	return layerSortedSet(set)
}

// layerSynopsis returns the first sentence of a package's doc comment, or "" if
// none. It reads the comment attached to the package clause (ast.File.Doc),
// choosing the lexically-first file that carries one so the result does not
// depend on go/packages' file ordering.
func layerSynopsis(p *packages.Package) string {
	if p == nil {
		return ""
	}
	type docFile struct {
		name string
		text string
	}
	var docs []docFile
	for _, f := range p.Syntax {
		if f.Doc == nil {
			continue
		}
		docs = append(docs, docFile{name: p.Fset.Position(f.Package).Filename, text: f.Doc.Text()})
	}
	if len(docs) == 0 {
		return ""
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].name < docs[j].name })
	return layerFirstSentence(docs[0].text)
}

// layerFirstSentence collapses a doc comment to its first sentence: the first
// paragraph, whitespace-normalized, truncated at the first sentence terminator
// that is not part of a known abbreviation.
func layerFirstSentence(doc string) string {
	doc = strings.TrimSpace(doc)
	if i := strings.Index(doc, "\n\n"); i >= 0 {
		doc = doc[:i]
	}
	doc = strings.Join(strings.Fields(doc), " ")
	for i := 0; i+1 < len(doc); i++ {
		if layerIsTerminator(doc[i]) && doc[i+1] == ' ' && !layerEndsWithAbbrev(doc[:i]) {
			return doc[:i+1]
		}
	}
	return doc
}

func layerIsTerminator(b byte) bool { return b == '.' || b == '!' || b == '?' }

// layerEndsWithAbbrev reports whether the token ending at s looks like a common
// abbreviation, so "e.g. " is not mistaken for a sentence end.
func layerEndsWithAbbrev(s string) bool {
	j := len(s)
	for j > 0 && (layerIsWordByte(s[j-1])) {
		j--
	}
	switch strings.ToLower(strings.TrimRight(s[j:], ".")) {
	case "e.g", "i.e", "etc", "cf", "vs", "al":
		return true
	}
	return false
}

func layerIsWordByte(b byte) bool {
	return b == '.' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// --- sorting helpers (all map iteration goes through these) -----------------

func layerSortedRuleNames(rules map[string]depguardRule) []string {
	out := make([]string, 0, len(rules))
	for k := range rules {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func layerSortedImportKeys(m map[string]*packages.Package) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func layerSortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func layerSortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func layerSortedKeysOfSets(m map[string]map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- rendering --------------------------------------------------------------

// layerBuilder is a tiny markdown accumulator. Its methods are scoped to the
// type, so they do not collide with the other generators in package main.
type layerBuilder struct{ strings.Builder }

// line writes one rendered line. Trailing horizontal whitespace is stripped
// because the repo's pre-commit trailing-whitespace hook would strip it from the
// committed file, after which the staleness gate would compare hook-stripped
// output against un-stripped generator output and fail on every subsequent run.
// The generator and that hook have to agree.
func (w *layerBuilder) line(format string, args ...any) {
	s := format
	if len(args) > 0 {
		s = fmt.Sprintf(format, args...)
	}
	w.WriteString(strings.TrimRight(s, " \t"))
	w.WriteByte('\n')
}

func (w *layerBuilder) blank() { w.WriteByte('\n') }

// layersMissingConfigBody is the deterministic fallback when .golangci.yml is
// absent, so the generator never errors on a bare tree.
func layersMissingConfigBody() string {
	return "## Architecture: Package Layers\n\n" +
		"No `" + golangciConfigFile + "` was found at the repository root, so the enforced " +
		"package-layer boundaries could not be read. Restore the linter config to regenerate " +
		"this reference.\n"
}

// renderLayerDoc renders the full body (no banner, no preamble — common.go adds
// those). The output is a deterministic walk of the pre-sorted model.
func renderLayerDoc(m layerModel) string {
	w := &layerBuilder{}
	w.writeIntro(m)
	w.writeOverview(m)
	w.writeRuleDetails(m)
	w.writeCrossImports(m)
	w.writeGaps(m)
	return strings.TrimRight(w.String(), "\n") + "\n"
}

func (w *layerBuilder) writeIntro(m layerModel) {
	w.line("## Architecture: Package Layers")
	w.blank()
	w.line("Loom's internal packages are grouped into enforced layers by the depguard")
	w.line("linter (`%s`, `linters.settings.depguard.rules`). This document is generated", golangciConfigFile)
	w.line("from those rules and cross-checked against the module's real import graph, so")
	w.line("it always reflects what the build enforces.")
	w.blank()
	if m.direction != "" {
		w.line("The intended dependency direction, from the config: **%s**.", m.direction)
		w.blank()
	}
}

func (w *layerBuilder) writeOverview(m layerModel) {
	w.line("### Layer boundaries")
	w.blank()
	w.line("Each depguard rule isolates a set of packages (matched by file globs) and")
	w.line("forbids them from importing named packages. `lax` mode denies only the listed")
	w.line("packages; `strict` mode additionally restricts imports to an allow-list. The")
	w.line("*Governed* count is the tracked packages whose files the rule's globs match.")
	w.blank()
	w.line("| Rule | Mode | Governed packages | Denied imports |")
	w.line("|------|------|-------------------|----------------|")
	for _, r := range m.rules {
		w.line("| `%s` | %s | %d | %d |", r.name, layerDash(r.listMode), len(r.packages), len(r.denies))
	}
	w.blank()
	w.line("%d of %d first-party `internal/` packages are governed by a layer rule; the",
		m.governedPkgs, m.totalPkgs)
	w.line("remaining %d are not constrained by these rules (depguard isolates only the",
		m.totalPkgs-m.governedPkgs)
	w.line("enumerated packages).")
	w.blank()
}

func (w *layerBuilder) writeRuleDetails(m layerModel) {
	w.line("### Rules")
	w.blank()
	for _, r := range m.rules {
		w.writeRule(r)
	}
}

func (w *layerBuilder) writeRule(r layerRuleView) {
	w.line("#### `%s` (list-mode: %s)", r.name, layerDash(r.listMode))
	w.blank()
	w.line("Applies to files matching:")
	w.blank()
	for _, g := range r.files {
		w.line("- `%s`", g)
	}
	w.blank()
	if len(r.denies) > 0 {
		w.line("Forbids importing:")
		w.blank()
		for _, d := range r.denies {
			w.line("- `%s` — %s", layerShortPkg(d.Pkg), layerCell(d.Desc))
		}
		w.blank()
	}
	if len(r.allow) > 0 {
		w.line("Allows importing (strict allow-list):")
		w.blank()
		for _, a := range r.allow {
			w.line("- `%s`", layerShortPkg(a))
		}
		w.blank()
	}
	w.writeRulePackages(r)
}

func (w *layerBuilder) writeRulePackages(r layerRuleView) {
	if len(r.packages) == 0 {
		w.line("Governs no tracked package (its globs match nothing today).")
		w.blank()
		return
	}
	w.line("Governs %d tracked package(s):", len(r.packages))
	w.blank()
	for _, p := range r.packages {
		if p.purpose == "" {
			w.line("- `%s`", p.path)
			continue
		}
		w.line("- `%s` — %s", p.path, layerCell(p.purpose))
	}
	w.blank()
}

func (w *layerBuilder) writeCrossImports(m layerModel) {
	w.line("### Actual cross-layer imports")
	w.blank()
	w.line("Computed from the loaded import graph (git-tracked `internal/` only). For each")
	w.line("rule, the other rules whose packages its own packages actually import:")
	w.blank()
	for _, c := range m.crossImports {
		if len(c.targets) == 0 {
			w.line("- `%s` → (none — imports no other governed layer)", c.name)
			continue
		}
		w.line("- `%s` → %s", c.name, layerJoinCode(c.targets))
	}
	w.blank()
	w.writeViolations(m)
}

func (w *layerBuilder) writeViolations(m layerModel) {
	w.line("**Forbidden-import check.**")
	w.blank()
	if len(m.violations) == 0 {
		w.line("No governed package imports a package its own rule denies — the real graph")
		w.line("agrees with the enforced boundaries.")
		w.blank()
		return
	}
	w.line("These real import edges violate a governing rule (golangci-lint should already")
	w.line("be failing on them):")
	w.blank()
	w.line("| Package | Imports | Rule | Reason |")
	w.line("|---------|---------|------|--------|")
	for _, v := range m.violations {
		w.line("| `%s` | `%s` | `%s` | %s |", v.from, v.imp, v.rule, layerCell(v.desc))
	}
	w.blank()
}

func (w *layerBuilder) writeGaps(m layerModel) {
	if len(m.externalRefs) == 0 && len(m.staleDenies) == 0 && len(m.deadGlobs) == 0 &&
		len(m.excludedPkgs) == 0 && len(m.ungovernedPkgs) == 0 {
		return
	}
	w.line("### Gaps and drift")
	w.blank()
	w.line("Findings where the enforced config and the real tree disagree.")
	w.blank()
	w.writeExternalRefs(m)
	w.writeUngoverned(m)
	w.writeStaleDenies(m)
	w.writeDeadGlobs(m)
	w.writeExcluded(m)
}

func (w *layerBuilder) writeUngoverned(m layerModel) {
	if len(m.ungovernedPkgs) == 0 {
		return
	}
	w.line("**Ungoverned packages.** No rule's file globs match these %d of %d `internal/`",
		len(m.ungovernedPkgs), m.totalPkgs)
	w.line("packages, so depguard checks none of their imports and the \"enforced, not")
	w.line("advisory\" guarantee above does not reach them. The depguard config asks that")
	w.line("new packages be added to the appropriate rule; these are the ones that were")
	w.line("not. A green lint run says nothing about any edge listed here:")
	w.blank()
	for _, p := range m.ungovernedPkgs {
		w.line("- `%s`", p)
	}
	w.blank()
}

func (w *layerBuilder) writeExcluded(m layerModel) {
	if len(m.excludedPkgs) == 0 {
		return
	}
	w.line("**Governed but lint-excluded.** `linters.exclusions.paths` (%s) removes these",
		layerJoinCode(m.excludedPaths))
	w.line("paths from every linter, depguard included. The packages below match a rule's")
	w.line("file globs — so they appear under a rule above — but depguard never actually")
	w.line("checks them, so that coverage is nominal:")
	w.blank()
	for _, p := range m.excludedPkgs {
		w.line("- `%s`", p)
	}
	w.blank()
}

func (w *layerBuilder) writeExternalRefs(m layerModel) {
	if len(m.externalRefs) == 0 {
		return
	}
	w.line("**Layer definitions referenced outside the repository.** The depguard config")
	w.line("cites these absolute paths, which are outside the repository tree and therefore")
	w.line("not version-controlled with the rules they explain — the rationale for the")
	w.line("boundaries cannot be regenerated or reviewed from this repo:")
	w.blank()
	for _, ref := range m.externalRefs {
		w.line("- `%s`", ref)
	}
	w.blank()
}

func (w *layerBuilder) writeStaleDenies(m layerModel) {
	if len(m.staleDenies) == 0 {
		return
	}
	w.line("**Stale deny targets.** These import paths are denied by depguard rules, but no")
	w.line("git-tracked package provides them (removed, renamed, or not yet added), so the")
	w.line("deny entries protect nothing today:")
	w.blank()
	for _, s := range m.staleDenies {
		w.line("- `%s` — referenced by %s", layerShortPkg(s.pkg), layerJoinCode(s.rules))
	}
	w.blank()
}

func (w *layerBuilder) writeDeadGlobs(m layerModel) {
	if len(m.deadGlobs) == 0 {
		return
	}
	w.line("**Dead file globs.** These rule globs match no git-tracked package:")
	w.blank()
	for _, g := range m.deadGlobs {
		w.line("- `%s`: `%s`", g.rule, g.glob)
	}
	w.blank()
}

// --- render helpers ---------------------------------------------------------

// layerShortPkg trims the module prefix from an import path for display, leaving
// internal/... where possible.
func layerShortPkg(pkg string) string {
	if i := strings.Index(pkg, "/internal/"); i >= 0 {
		return pkg[i+1:]
	}
	return pkg
}

// layerJoinCode renders a sorted slice as comma-separated inline code, leaving
// the "(ungoverned)" bucket unquoted.
func layerJoinCode(items []string) string {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		if strings.HasPrefix(it, "(") {
			parts = append(parts, it)
			continue
		}
		parts = append(parts, "`"+it+"`")
	}
	return strings.Join(parts, ", ")
}

// layerDash renders an empty field as an em dash.
func layerDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// layerCell makes text safe for a markdown table cell: single line, escaped
// pipes, em dash when empty.
func layerCell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "—"
	}
	return strings.ReplaceAll(s, "|", "\\|")
}
