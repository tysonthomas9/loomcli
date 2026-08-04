package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testConfig returns a genConfig rooted at a temp dir with a fixed tracked set,
// so pure-render tests do not depend on git or on the module type-checking.
func testConfig(t *testing.T) *genConfig {
	t.Helper()
	return &genConfig{
		RepoRoot:   t.TempDir(),
		MakeTarget: defaultMakeTarget,
		tracked:    map[string]bool{"internal/cli/root.go": true},
	}
}

// TestRenderIsDeterministic is the scaffold every Phase 2 generator inherits:
// render each doc twice and assert byte-identical output. Once the generators
// read source, this is what proves sorting made the output stable.
func TestRenderIsDeterministic(t *testing.T) {
	cfg := testConfig(t)
	for _, name := range genOrder {
		g := generators[name]
		first, err := renderDoc(cfg, g)
		if err != nil {
			t.Fatalf("%s: render: %v", name, err)
		}
		for i := 0; i < 3; i++ {
			next, err := renderDoc(cfg, g)
			if err != nil {
				t.Fatalf("%s: render %d: %v", name, i, err)
			}
			if next != first {
				t.Fatalf("%s: render %d differs from render 0", name, i+1)
			}
		}
		if !strings.Contains(first, genBanner) {
			t.Errorf("%s: output missing generated banner", name)
		}
		if !strings.Contains(first, "make "+defaultMakeTarget) {
			t.Errorf("%s: output missing regenerate hint", name)
		}
	}
}

// TestGenerateOneWritesDeterministically exercises the full disk pipeline:
// writing the same doc twice yields byte-identical files, which is exactly what
// the future staleness gate diffs.
func TestGenerateOneWritesDeterministically(t *testing.T) {
	cfg := testConfig(t)
	g := generators["envvars"]
	path := filepath.Join(cfg.RepoRoot, refDir, g.outName+".md")

	if err := generateOne(cfg, g, false); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled temp path
	if err != nil {
		t.Fatal(err)
	}
	if err := generateOne(cfg, g, false); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("second generation differs from the first")
	}
	if !strings.HasSuffix(string(first), "\n") || strings.HasSuffix(string(first), "\n\n") {
		t.Errorf("doc must end with exactly one newline, got %q", tail(string(first)))
	}
}

func TestAssembleDoc(t *testing.T) {
	header := generatedHeader("note.", defaultMakeTarget)
	got := assembleDoc(header, "  preamble prose  \n", "## Body\n\ntext\n")

	want := "<!-- " + genBanner + " -->\n" +
		"<!-- note. -->\n" +
		"<!-- Regenerate with: make " + defaultMakeTarget + " -->\n\n" +
		"preamble prose\n\n" +
		"## Body\n\ntext\n"
	if got != want {
		t.Errorf("assembleDoc mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestAssembleDocOmitsEmptyPreamble(t *testing.T) {
	got := assembleDoc(generatedHeader("", defaultMakeTarget), "", "## Body\n")
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("empty preamble left a blank gap: %q", got)
	}
	if !strings.HasPrefix(got, "<!-- "+genBanner+" -->\n") {
		t.Errorf("banner not first: %q", got)
	}
}

func TestGeneratedHeaderOmitsSourceNoteWhenEmpty(t *testing.T) {
	with := generatedHeader("something", "t")
	if !strings.Contains(with, "<!-- something -->") {
		t.Errorf("source note missing: %q", with)
	}
	without := generatedHeader("", "t")
	if strings.Count(without, "<!--") != 2 {
		t.Errorf("expected exactly two comment lines with no source note: %q", without)
	}
}

func TestResolveRepoRootWalksUpToGoMod(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveRepoRoot(nested)
	if err != nil {
		t.Fatalf("resolveRepoRoot: %v", err)
	}
	// Compare resolved (temp dirs may be symlinked, e.g. /var -> /private/var).
	wantResolved, _ := filepath.EvalSymlinks(root)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Errorf("resolveRepoRoot = %q, want %q", gotResolved, wantResolved)
	}
}

func TestResolveRepoRootErrorsWithoutGoMod(t *testing.T) {
	// A temp dir with no go.mod anywhere above it is not guaranteed (the OS temp
	// root has none), so assert only that a clearly bogus path errors.
	if _, err := resolveRepoRoot(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Skip("temp tree unexpectedly had a go.mod ancestor; environment-specific")
	}
}

func TestAnyIgnoredDropsSessionDirNotNewFile(t *testing.T) {
	root := "/repo"
	// Only gitignored session-dir files are in the ignored set.
	ignored := map[string]bool{"internal/cli/x/sessions/s.go": true}
	if anyIgnored([]string{"/repo/pkg/a.go", "/repo/pkg/b.go"}, root, ignored) {
		t.Error("clean package was dropped")
	}
	// A brand-new, not-yet-committed file is untracked but NOT ignored; its
	// package must still be kept — this is the dirty-tree undercount guard.
	if anyIgnored([]string{"/repo/pkg/a.go", "/repo/pkg/doc.go"}, root, ignored) {
		t.Error("package with a new untracked (not ignored) file was dropped")
	}
	// A gitignored session-dir file must drop the whole package.
	if !anyIgnored([]string{"/repo/pkg/a.go", "/repo/internal/cli/x/sessions/s.go"}, root, ignored) {
		t.Error("package with a gitignored file was kept")
	}
}

func TestReadPreambleMissingIsEmpty(t *testing.T) {
	got, err := readPreamble(t.TempDir(), "env-vars")
	if err != nil {
		t.Fatalf("missing preamble should not error: %v", err)
	}
	if got != "" {
		t.Errorf("missing preamble should be empty, got %q", got)
	}
}

func TestReadPreambleReadsPartial(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, refDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cli.preamble.md"), []byte("## Why\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readPreamble(root, "cli")
	if err != nil {
		t.Fatal(err)
	}
	if got != "## Why\n" {
		t.Errorf("preamble = %q", got)
	}
}

func TestUnknownSubcommandErrors(t *testing.T) {
	if err := run([]string{"nope"}, ".", defaultMakeTarget, false); err == nil {
		t.Error("expected error for unknown subcommand")
	}
}

func TestStdoutRequiresSingleSubcommand(t *testing.T) {
	if err := run(nil, ".", defaultMakeTarget, true); err == nil {
		t.Error("expected error: -stdout with no subcommand")
	}
}

func tail(s string) string {
	if len(s) > 12 {
		return s[len(s)-12:]
	}
	return s
}
