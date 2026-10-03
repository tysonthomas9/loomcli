package git

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/gitread"
)

// The task diff is what the task's PR contains: its newest revision against
// the layer below it in the lead's stack (trunk for the bottom layer), or
// against its own base while it is not applied.
func TestTaskDiffMatchesPRDiffPerLayer(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary repository builds the lead stack.
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(message string) string {
		t.Helper()
		git("add", "-A")
		git("commit", "-qm", message)
		return git("rev-parse", "HEAD")
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.test")
	write("f", "1\n2\n3\n")
	trunk := commit("trunk")
	storePath := filepath.Join(configDir, "loomgit", "store.db")
	freeze := func(task, attempt, base string, change func()) loomgit.Revision {
		t.Helper()
		git("checkout", "-q", "--detach", base)
		change()
		git("add", "-A")
		patch := git("diff", "--cached", "--binary", base)
		git("reset", "-q", "--hard")
		rev, err := driverfreeze.FreezeAt(context.Background(), storePath, driverfreeze.Request{
			Workspace: "W", Task: task, Repo: "repo", Attempt: attempt, Worktree: repo, Base: base,
			Patch: []byte(patch + "\n"), Outcome: "completed", AuthorKind: "agent", AuthorID: "worker",
		})
		if err != nil {
			t.Fatal(err)
		}
		return rev
	}
	// The middle task started on trunk and changes line 3.
	mid := freeze("MID", "mid", trunk, func() { write("f", "1\n2\nthree\n") })
	// The bottom task then changed line 1: the layer below the middle task
	// moved after it started.
	low := freeze("LOW", "low", trunk, func() { write("f", "one\n2\n3\n") })
	top := freeze("TOP", "top", trunk, func() { write("top", "top\n") })
	loose := freeze("LOOSE", "loose1", trunk, func() { write("loose", "a\n") })
	// UP is applied above the lead's own commit, which is not a task layer.
	up := freeze("UP", "up", trunk, func() { write("up", "up\n") })
	retried := freeze("LOOSE", "loose2", trunk, func() { write("loose", "b\n") })

	// Lead stack: LOW, MID (rebuilt on LOW), TOP.
	git("checkout", "-q", "-b", "lead", trunk)
	write("f", "one\n2\n3\n")
	lowTip := commit("low")
	write("f", "one\n2\nthree\n")
	midTip := commit("mid")
	write("top", "top\n")
	topTip := commit("top")
	write("own", "lead edit\n")
	ownTip := commit("own")
	write("up", "up\n")
	upTip := commit("up")

	db, err := sql.Open("sqlite", storePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	run := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	run(`INSERT INTO workspace_repos (workspace,repo,trunk,workspace_branch,base_sha) VALUES ('W','repo','main','lead',?)`, trunk)
	run(`INSERT INTO working_areas (workspace,lead,repo,path,branch,base_sha,mode) VALUES ('W','lead','repo',?,'lead',?,'interactive')`, repo, trunk)
	for i, layer := range []struct {
		rev      loomgit.Revision
		old, tip string
	}{{low, trunk, lowTip}, {mid, lowTip, midTip}, {top, midTip, topTip}, {up, ownTip, upTip}} {
		run(`INSERT INTO applied_layers (request_id,workspace,lead,change_id,revision,old_tip,new_tip,commits,dropped,phase)
			VALUES (?,'W','lead',?,?,?,?,'[]','[]','done')`, "req-"+string(rune('a'+i)), layer.rev.Change, layer.rev.Number, layer.old, layer.tip)
	}

	// LOOSE's first revision is applied in another lead; its newest is not, so
	// the task diff is the newest revision against its own base.
	run(`INSERT INTO working_areas (workspace,lead,repo,path,branch,base_sha,mode) VALUES ('W','other','repo',?,'other',?,'interactive')`, repo, trunk)
	run(`INSERT INTO applied_layers (request_id,workspace,lead,change_id,revision,old_tip,new_tip,commits,dropped,phase)
		VALUES ('req-loose','W','other',?,?,?,?,'[]','[]','done')`, loose.Change, loose.Number, trunk, loose.HeadSHA)

	open := func() (*gitread.Reader, func() error, error) {
		return gitread.OpenLocal(func(string, string) string { return repo })
	}
	previousReader := openRevisionReader
	openRevisionReader = open
	t.Cleanup(func() { openRevisionReader = previousReader })
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/diff", handleTaskDiff(open))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/revisions", handleTaskRevisions)
	taskDiff := func(task string) gitread.TaskDiff {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces/W/issues/"+task, nil))
		var body struct {
			Success bool             `json:"success"`
			Data    gitread.TaskDiff `json:"data"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || !body.Success {
			t.Fatalf("%s diff: %d %s", task, rec.Code, rec.Body.String())
		}
		return body.Data
	}
	patches := func(d gitread.TaskDiff) string {
		var out []string
		for _, f := range d.Files {
			out = append(out, f.Patch)
		}
		return strings.Join(out, "")
	}
	prDiff := func(from, to string) string {
		return git("diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-color", "--patch", from, to) + "\n"
	}
	cases := []struct {
		task, compare, want string
		rev                 loomgit.Revision
	}{
		{"MID/diff", "layer", prDiff(lowTip, midTip), mid},
		{"MID/diff?lead=lead", "layer", prDiff(lowTip, midTip), mid},
		{"LOW/diff", "trunk", prDiff(trunk, lowTip), low},
		{"LOW/diff?lead=lead", "trunk", prDiff(trunk, lowTip), low},
		{"UP/diff", "layer", prDiff(ownTip, upTip), up},
		{"LOOSE/diff", "base", prDiff(retried.BaseSHA, retried.HeadSHA), retried},
	}
	for _, tc := range cases {
		got := taskDiff(tc.task)
		if got.Compare != tc.compare || got.Repo != "repo" || got.Change != tc.rev.Change || got.Revision != tc.rev.Number {
			t.Fatalf("%s: compare=%q repo=%q change=%q revision=%d", tc.task, got.Compare, got.Repo, got.Change, got.Revision)
		}
		if patches(got) != tc.want {
			t.Fatalf("%s: task diff differs from PR diff\n got: %q\nwant: %q", tc.task, patches(got), tc.want)
		}
	}
	// The middle task's own revision diff carries the old line 1 context, so
	// the PR diff above is not the revision's base diff.
	if own := prDiff(mid.BaseSHA, mid.HeadSHA); own == prDiff(lowTip, midTip) {
		t.Fatal("fixture does not distinguish the layer diff from the revision diff")
	}

	// PR per task (trunk mode): every task's PR is its own revision on trunk,
	// so the middle task no longer compares with the layer below.
	run(`INSERT INTO workspace_settings (workspace,delivery_mode) VALUES ('W','trunk')`)
	if got := taskDiff("MID/diff"); got.Compare != "trunk" || patches(got) != prDiff(mid.BaseSHA, mid.HeadSHA) {
		t.Fatalf("trunk mode MID: compare=%q patch=%q", got.Compare, patches(got))
	}
	run(`UPDATE workspace_settings SET delivery_mode='stack' WHERE workspace='W'`)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces/W/issues/LOOSE/revisions", nil))
	var list struct {
		Data []struct {
			Number     int    `json:"number"`
			Repo       string `json:"repo"`
			Superseded bool   `json:"superseded"`
			Date       string `json:"date"`
		} `json:"data"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Data) != 2 {
		t.Fatalf("revisions: %d %s", rec.Code, rec.Body.String())
	}
	for _, item := range list.Data {
		if item.Repo != "repo" || item.Superseded != (item.Number == loose.Number) || item.Date == "" {
			t.Fatalf("revision %d: repo=%q superseded=%t date=%q", item.Number, item.Repo, item.Superseded, item.Date)
		}
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces/W/issues/NONE/diff", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not_found") {
		t.Fatalf("no revisions: %d %s", rec.Code, rec.Body.String())
	}
}
