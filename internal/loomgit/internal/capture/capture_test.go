package capture

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

func fixture(t *testing.T) (string, *gitexec.Runner) {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	config := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(config, []byte("[user]\nname = Test\nemail = test@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := gitexec.New(dir, gitexec.Options{GlobalConfig: config, SystemConfig: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "base.txt", "base")
	must(t, r, "add", "base.txt")
	must(t, r, "commit", "-qm", "base")
	return dir, r
}

func write(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func must(t *testing.T, r *gitexec.Runner, args ...string) string {
	t.Helper()
	out, err := r.Run(context.Background(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func capture(t *testing.T, dir string, r *gitexec.Runner) Result {
	t.Helper()
	result, err := Capture(context.Background(), r, dir, Params{Workspace: "ws", Attempt: "a1", TaskID: "task-1", TaskTitle: "Build capture"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func classes(entries []Entry) map[string]Entry {
	m := make(map[string]Entry)
	for _, entry := range entries {
		m[entry.Path] = entry
	}
	return m
}

func TestCapturePreservesIndexAndCapturesLargeTrackedEdit(t *testing.T) {
	dir, r := fixture(t)
	write(t, dir, "tracked.bin", "before")
	must(t, r, "add", "tracked.bin")
	must(t, r, "commit", "-qm", "tracked")
	write(t, dir, "tracked.bin", strings.Repeat("x", 20<<20))
	write(t, dir, "new.txt", "new")
	write(t, dir, "y.rs", "staged")
	must(t, r, "add", "y.rs")
	beforeIndex := must(t, r, "ls-files", "--stage")
	beforeHead := must(t, r, "rev-parse", "HEAD")
	result := capture(t, dir, r)
	if !result.Manifest.Complete || result.CaptureSHA == "" {
		t.Fatalf("capture incomplete: %+v", result)
	}
	if got := must(t, r, "ls-files", "--stage"); got != beforeIndex {
		t.Fatal("user index changed")
	}
	if got := must(t, r, "rev-parse", "HEAD"); got != beforeHead {
		t.Fatal("HEAD changed")
	}
	if got := must(t, r, "rev-parse", result.CaptureSHA+"^"); got != beforeHead {
		t.Fatalf("capture parent %s, want %s", got, beforeHead)
	}
	if got := must(t, r, "cat-file", "-s", result.CaptureSHA+":tracked.bin"); got != "20971520" {
		t.Fatalf("tracked size = %s", got)
	}
	for _, path := range []string{"new.txt", "y.rs"} {
		must(t, r, "cat-file", "-e", result.CaptureSHA+":"+path)
	}
	if _, err := os.Stat(result.ManifestPath); err != nil {
		t.Fatal(err)
	}
	if got := must(t, r, "show", "-s", "--format=%B", result.CaptureSHA); !strings.Contains(got, "Loom-Task: task-1") || !strings.Contains(got, "Loom-Attempt: a1") {
		t.Fatal(got)
	}
}

func TestCaptureListsIgnoredAndRejectsSecrets(t *testing.T) {
	dir, r := fixture(t)
	write(t, dir, ".gitignore", ".env\nnode_modules/\n")
	write(t, dir, ".env", strings.Repeat("x", 200<<10))
	write(t, dir, "node_modules/pkg/index.js", "package")
	for _, path := range []string{".env.local", "server.pem", "id_ed25519"} {
		write(t, dir, path, "private")
	}
	result := capture(t, dir, r)
	if result.Manifest.Complete || !result.Manifest.Retained {
		t.Fatalf("secret capture reported complete: %+v", result.Manifest)
	}
	entries := classes(result.Manifest.Entries)
	if entries[".env"].Class != Listed || entries[".env"].Size != 200<<10 || entries["node_modules/"].Class != Listed || entries["node_modules/"].Size != int64(len("package")) {
		t.Fatalf("ignored entries: %+v", entries)
	}
	for _, path := range []string{".env.local", "server.pem", "id_ed25519"} {
		if entries[path].Class != SecretSuspect {
			t.Fatalf("%s: %+v", path, entries[path])
		}
	}
	if result.CaptureSHA == "" {
		t.Fatal("expected .gitignore capture")
	}
	paths := must(t, r, "ls-tree", "-r", "--name-only", result.CaptureSHA)
	for _, path := range strings.Split(paths, "\n") {
		if SecretPath(path) || path == "node_modules/pkg/index.js" {
			t.Fatalf("forbidden path in capture: %s", path)
		}
	}
}

func TestIgnoredOnlyCaptureIsComplete(t *testing.T) {
	dir, r := fixture(t)
	write(t, dir, ".gitignore", ".env\nnode_modules/\n")
	must(t, r, "add", ".gitignore")
	must(t, r, "commit", "-qm", "ignore rules")
	write(t, dir, ".env", strings.Repeat("x", 200<<10))
	write(t, dir, "node_modules/pkg/index.js", "package")
	result := capture(t, dir, r)
	entries := classes(result.Manifest.Entries)
	if !result.Manifest.Complete || result.Manifest.Retained || result.CaptureSHA != "" {
		t.Fatalf("ignored-only capture: %+v", result)
	}
	if entries[".env"].Class != Listed || entries[".env"].Size != 200<<10 || entries["node_modules/"].Class != Listed {
		t.Fatalf("ignored entries: %+v", entries)
	}
}

func TestTrackedSecretIsRemovedFromCaptureTree(t *testing.T) {
	dir, r := fixture(t)
	write(t, dir, "credentials.json", "private")
	must(t, r, "add", "credentials.json")
	must(t, r, "commit", "-qm", "legacy secret")
	result := capture(t, dir, r)
	if result.CaptureSHA == "" || result.Manifest.Complete || classes(result.Manifest.Entries)["credentials.json"].Class != SecretSuspect {
		t.Fatalf("tracked secret result: %+v", result)
	}
	if got := must(t, r, "ls-tree", "-r", "--name-only", result.CaptureSHA); strings.Contains(got, "credentials.json") {
		t.Fatalf("secret still in capture tree: %s", got)
	}
}

func TestCaptureRecordsSizeAndPermissionFailures(t *testing.T) {
	dir, r := fixture(t)
	full := filepath.Join(dir, "large.bin")
	f, err := os.Create(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	write(t, dir, "unreadable.txt", "private")
	if err := os.Chmod(filepath.Join(dir, "unreadable.txt"), 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "unreadable.txt"), 0600) })
	result := capture(t, dir, r)
	entries := classes(result.Manifest.Entries)
	if result.Manifest.Complete || !result.Manifest.Retained || entries["large.bin"].Class != Incomplete || entries["unreadable.txt"].Class != Incomplete {
		t.Fatalf("incomplete entries: %+v", result.Manifest)
	}
}

func TestCaptureCleanTreeMakesNoCommit(t *testing.T) {
	dir, r := fixture(t)
	result := capture(t, dir, r)
	if result.CaptureSHA != "" || !result.Manifest.Complete {
		t.Fatalf("clean result: %+v", result)
	}
}

func TestFreezeSourceRewritesChainAndKeepsCapture(t *testing.T) {
	dir, r := fixture(t)
	base := must(t, r, "rev-parse", "HEAD")
	write(t, dir, "c1", "one")
	must(t, r, "add", "c1")
	must(t, r, "commit", "-qm", "agent c1")
	write(t, dir, "c2", "two")
	must(t, r, "add", "c2")
	must(t, r, "commit", "-qm", "agent c2")
	write(t, dir, "edit", "three")
	result := capture(t, dir, r)
	frozen, err := FreezeSource(context.Background(), r, FreezeParams{Workspace: "ws", Attempt: "a1", TaskID: "task-1", ChangeID: "change-1", Revision: "1", BaseSHA: base, HeadSHA: result.CaptureSHA})
	if err != nil {
		t.Fatal(err)
	}
	if got := must(t, r, "rev-parse", result.CaptureRef); got != result.CaptureSHA {
		t.Fatal("original capture ref moved")
	}
	if got := must(t, r, "rev-parse", "refs/loom/ws/ws/change/change-1/1/head"); got != frozen {
		t.Fatal("source revision head mismatch")
	}
	commits := strings.Fields(must(t, r, "rev-list", "--reverse", base+".."+frozen))
	if len(commits) != 3 {
		t.Fatalf("got %d commits", len(commits))
	}
	for i, sha := range commits {
		message := must(t, r, "show", "-s", "--format=%B", sha)
		for _, trailer := range []string{"Loom-Task: task-1", "Loom-Attempt: a1", "Loom-Change-Id: change-1", "Loom-Revision: 1"} {
			if !strings.Contains(message, trailer) {
				t.Fatalf("commit %d missing %s: %s", i, trailer, message)
			}
		}
	}
	if msg := must(t, r, "show", "-s", "--format=%s", commits[0]); msg != "agent c1" {
		t.Fatal(msg)
	}
	if msg := must(t, r, "show", "-s", "--format=%s", commits[1]); msg != "agent c2" {
		t.Fatal(msg)
	}
	if got, want := must(t, r, "rev-parse", frozen+"^{tree}"), must(t, r, "rev-parse", result.CaptureSHA+"^{tree}"); got != want {
		t.Fatalf("tree differs: %s vs %s", got, want)
	}
}
