package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

func TestParsePorcelainV1ZUnusualPathsAndRenameSource(t *testing.T) {
	paths := []string{"line\nbreak", `quote"slash\\`, " leading ", "unicode-雪", "literal -> arrow"}
	var raw strings.Builder
	for _, path := range paths {
		raw.WriteString("?? ")
		raw.WriteString(path)
		raw.WriteByte(0)
	}
	raw.WriteString("R  destination -> literal")
	raw.WriteByte(0)
	raw.WriteString("source\nname")
	raw.WriteByte(0)
	result := parsePorcelainV1Z([]byte(raw.String()), 50_000)
	if result.Partial || result.LimitHit {
		t.Fatalf("unexpected bounds: %+v", result)
	}
	for _, path := range paths {
		if result.Entries[path] != "??" {
			t.Fatalf("status[%q] = %q", path, result.Entries[path])
		}
	}
	if result.Entries["destination -> literal"] != "R " {
		t.Fatalf("rename destination missing: %#v", result.Entries)
	}
	if _, ok := result.Entries["source\nname"]; ok {
		t.Fatalf("rename source was treated as destination: %#v", result.Entries)
	}
}

func TestParsePorcelainV1ZEntryCapIsExplicit(t *testing.T) {
	result := parsePorcelainV1Z([]byte("?? one\x00?? two\x00"), 1)
	if !result.Partial || !result.LimitHit || len(result.Entries) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestIsolatedGitEnvDropsInheritedGitVariablesAndPinsLocale(t *testing.T) {
	env := isolatedGitEnv([]string{"PATH=/bin", "GIT_DIR=/tmp/evil", "git_config_count=1", "HOME=/home/test", "LANG=fr_FR.UTF-8", "LC_ALL=de_DE.UTF-8"})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "GIT_DIR=/tmp/evil") || strings.Contains(strings.ToUpper(joined), "GIT_CONFIG_COUNT=1") {
		t.Fatalf("inherited git environment survived: %s", joined)
	}
	if strings.Contains(joined, "fr_FR") || strings.Contains(joined, "de_DE") {
		t.Fatalf("inherited locale survived: %s", joined)
	}
	for _, required := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "LANG=C", "LC_ALL=C"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing %q in %s", required, joined)
		}
	}
}

func TestGitInspectorAddsSafeDirectoryForValidatedCheckout(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	fake := writeInspectorScript(t, "printf '%s\n' \"$@\" > \""+argsPath+"\"")
	inspector := &GitInspector{binary: fake}
	if _, err := inspector.Status(context.Background(), dir); err != nil {
		t.Fatalf("Status: %v", err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	if !strings.Contains(string(args), "safe.directory="+dir) {
		t.Fatalf("args = %s, missing safe.directory for %s", args, dir)
	}
}

func TestGitInspectorRejectsChangedCheckoutIdentity(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "checkout")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(parent, "checkout-old")
	if err := os.Rename(dir, oldDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "ran")
	fake := writeInspectorScript(t, "touch \""+sentinel+"\"")
	ctx := ops.WithGitWorktreeIdentity(context.Background(), dir, info)
	_, err = (&GitInspector{binary: fake}).Status(ctx, dir)
	if inspectionKind(err) != "validation" {
		t.Fatalf("Status error = %T %v, want validation", err, err)
	}
	if _, statErr := os.Stat(sentinel); !os.IsNotExist(statErr) {
		t.Fatalf("git executed after identity change: %v", statErr)
	}
}

func TestGitInspectorRejectsOptionAndRangeRevisionsBeforeExec(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "ran")
	fake := writeInspectorScript(t, "touch \""+sentinel+"\"\nexit 0")
	inspector := &GitInspector{binary: fake}
	for _, rev := range []string{"--output=/tmp/sentinel", "main..other", "bad:rev", "white space"} {
		if _, err := inspector.Show(context.Background(), t.TempDir(), rev, "file.txt", 10); inspectionKind(err) != "validation" {
			t.Fatalf("Show accepted revision %q", rev)
		}
		if _, err := inspector.Diff(context.Background(), t.TempDir(), "file.txt", rev, "HEAD"); inspectionKind(err) != "validation" {
			t.Fatalf("Diff accepted revision %q", rev)
		}
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("git executed for invalid revision: %v", err)
	}
}

func TestGitInspectorShowClassifiesMissingRevisionPath(t *testing.T) {
	dir := t.TempDir()
	mustInspectorGit(t, dir, "init")
	mustInspectorGit(t, dir, "config", "user.email", "test@example.com")
	mustInspectorGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("tracked\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mustInspectorGit(t, dir, "add", "tracked.txt")
	mustInspectorGit(t, dir, "commit", "-m", "seed")
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("untracked\n"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := NewGitInspector().Show(context.Background(), dir, "HEAD", "untracked.txt", 1024)
	if got := inspectionKind(err); got != "not_found" {
		t.Fatalf("inspection kind = %q, error = %v", got, err)
	}
}

func inspectionKind(err error) string {
	var inspectorErr *InspectorError
	if errors.As(err, &inspectorErr) {
		return inspectorErr.InspectionKind()
	}
	return ""
}

func TestGitInspectorTimeoutIsTyped(t *testing.T) {
	fake := writeInspectorScript(t, "sleep 5")
	inspector := &GitInspector{binary: fake}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := inspector.Status(ctx, t.TempDir())
	var inspectorErr *InspectorError
	if !errors.As(err, &inspectorErr) || inspectorErr.Kind != "timeout" {
		t.Fatalf("error = %T %v, want typed timeout", err, err)
	}
}

func TestGitInspectorOutputCapIsExplicit(t *testing.T) {
	fake := writeInspectorScript(t, "while :; do dd if=/dev/zero bs=1048576 count=1 2>/dev/null; done")
	started := time.Now()
	result, err := (&GitInspector{binary: fake}).Status(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !result.Partial || !result.LimitHit {
		t.Fatalf("result = %+v", result)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("status producer was not terminated promptly: %v", elapsed)
	}

	started = time.Now()
	text, err := (&GitInspector{binary: fake}).Diff(context.Background(), t.TempDir(), "file.txt", "HEAD", "")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !text.Partial || !text.LimitHit || len(text.Output) != fileDiffBytes {
		t.Fatalf("text result = bytes:%d partial:%v limit:%v", len(text.Output), text.Partial, text.LimitHit)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("text producer was not terminated promptly: %v", elapsed)
	}
}

func TestGitInspectorStatusPreservesExactXYForRealUnusualPaths(t *testing.T) {
	dir := t.TempDir()
	mustInspectorGit(t, dir, "init")
	paths := []string{"line\nbreak", `quote"slash\\`, " leading ", "unicode-雪", "literal -> arrow"}
	for _, path := range paths {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(path), 0600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := NewGitInspector().Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, path := range paths {
		if result.Entries[path] != "??" {
			t.Fatalf("status[%q] = %q; all=%#v", path, result.Entries[path], result.Entries)
		}
	}
}

func TestGitInspectorDisablesExternalHelpersAndInheritedGitDir(t *testing.T) {
	dir := t.TempDir()
	mustInspectorGit(t, dir, "init")
	mustInspectorGit(t, dir, "config", "user.email", "test@example.com")
	mustInspectorGit(t, dir, "config", "user.name", "Test")
	file := filepath.Join(dir, "file.custom")
	if err := os.WriteFile(file, []byte("one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mustInspectorGit(t, dir, "add", "file.custom")
	mustInspectorGit(t, dir, "commit", "-m", "init")
	if err := os.WriteFile(file, []byte("two\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "helper-ran")
	helper := writeInspectorScript(t, "touch \""+sentinel+"\"")
	mustInspectorGit(t, dir, "config", "core.fsmonitor", helper)
	mustInspectorGit(t, dir, "config", "diff.external", helper)
	mustInspectorGit(t, dir, "config", "diff.custom.textconv", helper)
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.custom diff=custom\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "evil"))
	inspector := NewGitInspector()
	if _, err := inspector.Status(context.Background(), dir); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, err := inspector.Diff(context.Background(), dir, "file.custom", "HEAD", ""); err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if _, err := inspector.Blame(context.Background(), dir, "file.custom"); err != nil {
		t.Fatalf("Blame: %v", err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("external helper executed: %v", err)
	}
}

func writeInspectorScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-git")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustInspectorGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	result, err := NewGitInspector().run(context.Background(), "test", dir, 5*time.Second, 1024*1024, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, result.Output)
	}
}
