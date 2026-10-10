package driverfreeze_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

type secretRepo struct {
	t    *testing.T
	dir  string
	base string
}

func newSecretRepo(t *testing.T, files map[string]string) *secretRepo {
	t.Helper()
	r := &secretRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q")
	r.git("config", "user.name", "Test")
	r.git("config", "user.email", "test@example.test")
	files["README"] = "base\n"
	for name, body := range files {
		r.write(name, body)
	}
	r.git("add", "--all")
	r.git("commit", "-qm", "base")
	r.base = r.git("rev-parse", "HEAD")
	return r
}

func (r *secretRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary repository proves D18 on the flat-patch freeze.
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *secretRepo) write(name, body string) {
	r.t.Helper()
	path := filepath.Join(r.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// patch is the runner's flat patch: every working-tree change, including
// untracked and force-added ignored files, against base.
func (r *secretRepo) patch(force ...string) []byte {
	r.t.Helper()
	r.git("add", "--intent-to-add", "--all")
	for _, path := range force {
		r.git("add", "--force", "--intent-to-add", path)
	}
	out := r.git("diff", "--binary", r.base)
	r.git("reset", "-q")
	return []byte(out + "\n")
}

func (r *secretRepo) tree(rev string) []string {
	return strings.Split(r.git("ls-tree", "-r", "--name-only", rev), "\n")
}

func (r *secretRepo) manifest(attempt string) map[string]string {
	r.t.Helper()
	data, err := os.ReadFile(filepath.Join(r.dir, ".git", "loom", "capture", "WS-"+attempt+".json"))
	if err != nil {
		r.t.Fatalf("capture manifest: %v", err)
	}
	var manifest struct {
		Entries []struct{ Path, Class string }
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		r.t.Fatal(err)
	}
	classes := map[string]string{}
	for _, entry := range manifest.Entries {
		classes[entry.Path] = entry.Class
	}
	return classes
}

func retainedComplete(t *testing.T, journalPath, attempt string) bool {
	t.Helper()
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	rows, err := store.RetainedCopies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Attempt == attempt {
			return row.Complete
		}
	}
	t.Fatalf("no retained copy for %s", attempt)
	return false
}

func contains(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func TestSuccessfulFreezeNeverCapturesUntrackedSecretPath(t *testing.T) {
	r := newSecretRepo(t, map[string]string{"app.go": "base\n"})
	r.write("app.go", "edit\n")
	r.write("server.pem", "non-secret test marker\n")
	r.write("certs/nested/.env.local", "TOKEN=marker\n")
	journalPath := filepath.Join(t.TempDir(), "journal.db")
	rev, err := driverfreeze.FreezeAt(context.Background(), journalPath, driverfreeze.Request{
		Workspace: "WS", Task: "TASK", Repo: "repo", Attempt: "a1", Worktree: r.dir,
		Base: r.base, Patch: r.patch(), Outcome: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rev.Incomplete || rev.NoChanges || !rev.Ready || rev.Outcome != "completed" {
		t.Fatalf("revision = %+v, want ready incomplete completed revision", rev)
	}
	tree := r.tree(rev.HeadSHA)
	if contains(tree, "server.pem") || contains(tree, "certs/nested/.env.local") || !contains(tree, "app.go") {
		t.Fatalf("revision tree = %v", tree)
	}
	if got := r.git("show", rev.HeadSHA+":app.go"); got != "edit" {
		t.Fatalf("app.go = %q", got)
	}
	classes := r.manifest("a1")
	if classes["server.pem"] != "secret_suspect" || classes["certs/nested/.env.local"] != "secret_suspect" {
		t.Fatalf("manifest = %v", classes)
	}
	if retainedComplete(t, journalPath, "a1") {
		t.Fatal("task copy was recorded complete, so retention could remove the secret file")
	}
	if data, err := os.ReadFile(filepath.Join(r.dir, "server.pem")); err != nil || string(data) != "non-secret test marker\n" {
		t.Fatalf("task copy lost server.pem: %q, %v", data, err)
	}
	again, err := driverfreeze.FreezeAt(context.Background(), journalPath, driverfreeze.Request{
		Workspace: "WS", Task: "TASK", Repo: "repo", Attempt: "a1", Worktree: r.dir,
		Base: r.base, Patch: r.patch(), Outcome: "completed",
	})
	if err != nil || again.HeadSHA != rev.HeadSHA || !again.Incomplete {
		t.Fatalf("replay = %+v, %v", again, err)
	}
}

func TestSecretOnlyFreezeIsIncompleteNotNoChanges(t *testing.T) {
	r := newSecretRepo(t, map[string]string{})
	r.write("server.pem", "non-secret test marker\n")
	rev, err := driverfreeze.FreezeAt(context.Background(), filepath.Join(t.TempDir(), "journal.db"), driverfreeze.Request{
		Workspace: "WS", Task: "TASK", Repo: "repo", Attempt: "only", Worktree: r.dir,
		Base: r.base, Patch: r.patch(), Outcome: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rev.Incomplete || rev.NoChanges || contains(r.tree(rev.HeadSHA), "server.pem") {
		t.Fatalf("revision = %+v tree %v", rev, r.tree(rev.HeadSHA))
	}
}

func TestFailedAndTimedOutFreezesNeverCaptureUntrackedSecretPath(t *testing.T) {
	for _, outcome := range []string{"failed", "timeout", "cancelled"} {
		r := newSecretRepo(t, map[string]string{})
		r.write("id_ed25519", "non-secret test marker\n")
		r.write("work.txt", "work\n")
		rev, err := driverfreeze.FreezeAt(context.Background(), filepath.Join(t.TempDir(), "journal.db"), driverfreeze.Request{
			Workspace: "WS", Task: "TASK", Repo: "repo", Attempt: outcome, Worktree: r.dir,
			Base: r.base, Patch: r.patch(), Outcome: outcome,
		})
		if err != nil {
			t.Fatalf("%s: %v", outcome, err)
		}
		tree := r.tree(rev.HeadSHA)
		if !rev.Incomplete || contains(tree, "id_ed25519") || !contains(tree, "work.txt") {
			t.Fatalf("%s: revision = %+v tree %v", outcome, rev, tree)
		}
	}
}

func TestTrackedSecretPathIsStillCaptured(t *testing.T) {
	r := newSecretRepo(t, map[string]string{"config/server.pem": "base\n"})
	r.write("config/server.pem", "rotated\n")
	journalPath := filepath.Join(t.TempDir(), "journal.db")
	rev, err := driverfreeze.FreezeAt(context.Background(), journalPath, driverfreeze.Request{
		Workspace: "WS", Task: "TASK", Repo: "repo", Attempt: "tracked", Worktree: r.dir,
		Base: r.base, Patch: r.patch(), Outcome: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rev.Incomplete || r.git("show", rev.HeadSHA+":config/server.pem") != "rotated" {
		t.Fatalf("tracked secret path was not captured: %+v", rev)
	}
	if !retainedComplete(t, journalPath, "tracked") {
		t.Fatal("complete capture recorded as incomplete")
	}
	if _, err := os.Stat(filepath.Join(r.dir, ".git", "loom", "capture", "WS-tracked.json")); !os.IsNotExist(err) {
		t.Fatalf("complete flat patch wrote a manifest: %v", err)
	}
}

func TestAgentCommittedSecretPathIsTracked(t *testing.T) {
	r := newSecretRepo(t, map[string]string{})
	r.write("server.pem", "committed by agent\n")
	r.git("add", "server.pem")
	r.git("commit", "-qm", "agent commit")
	head := r.git("rev-parse", "HEAD")
	r.write("README", "edit\n")
	rev, err := driverfreeze.FreezeAt(context.Background(), filepath.Join(t.TempDir(), "journal.db"), driverfreeze.Request{
		Workspace: "WS", Task: "TASK", Repo: "repo", Attempt: "committed", Worktree: r.dir,
		Base: r.base, CommitHeadSHA: head, Patch: r.patch(), Outcome: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rev.Incomplete || !contains(r.tree(rev.HeadSHA), "server.pem") {
		t.Fatalf("agent-committed secret path should be tracked: %+v tree %v", rev, r.tree(rev.HeadSHA))
	}
}

func TestIgnoredSecretPathIsListedNotCaptured(t *testing.T) {
	r := newSecretRepo(t, map[string]string{".gitignore": "*.pem\n"})
	r.write("server.pem", "ignored marker\n")
	r.write("work.txt", "work\n")
	rev, err := driverfreeze.FreezeAt(context.Background(), filepath.Join(t.TempDir(), "journal.db"), driverfreeze.Request{
		Workspace: "WS", Task: "TASK", Repo: "repo", Attempt: "ignored", Worktree: r.dir,
		Base: r.base, Patch: r.patch("server.pem"), Outcome: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rev.Incomplete || contains(r.tree(rev.HeadSHA), "server.pem") || !contains(r.tree(rev.HeadSHA), "work.txt") {
		t.Fatalf("ignored secret: %+v tree %v", rev, r.tree(rev.HeadSHA))
	}
	if classes := r.manifest("ignored"); classes["server.pem"] != "listed" {
		t.Fatalf("manifest = %v", classes)
	}
}

func TestIgnoredNonSecretPathIsListedNotCaptured(t *testing.T) {
	r := newSecretRepo(t, map[string]string{".gitignore": "*.out\n"})
	r.write("kept.out", "tracked\n")
	r.git("add", "--force", "kept.out")
	r.git("commit", "-qm", "track an ignored-pattern file")
	r.base = r.git("rev-parse", "HEAD")
	r.write("generated.out", "ignored build output\n")
	r.write("kept.out", "tracked edit\n")
	r.write("work.txt", "work\n")
	journalPath := filepath.Join(t.TempDir(), "journal.db")
	rev, err := driverfreeze.FreezeAt(context.Background(), journalPath, driverfreeze.Request{
		Workspace: "WS", Task: "TASK", Repo: "repo", Attempt: "ignored-plain", Worktree: r.dir,
		Base: r.base, Patch: r.patch("generated.out"), Outcome: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	tree := r.tree(rev.HeadSHA)
	if rev.Incomplete || contains(tree, "generated.out") || !contains(tree, "work.txt") {
		t.Fatalf("ignored path: %+v tree %v", rev, tree)
	}
	if got := r.git("show", rev.HeadSHA+":kept.out"); got != "tracked edit" {
		t.Fatalf("tracked ignored-pattern file = %q, want captured edit", got)
	}
	if classes := r.manifest("ignored-plain"); classes["generated.out"] != "listed" || len(classes) != 1 {
		t.Fatalf("manifest = %v", classes)
	}
	if !retainedComplete(t, journalPath, "ignored-plain") {
		t.Fatal("listed ignored path made the capture incomplete")
	}
}
