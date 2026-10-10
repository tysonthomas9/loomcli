package agentsv1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentworktree"
	"github.com/tysonthomas9/loomcli/internal/gitrunner"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// turnRig is a real repo and worktree port behind the Agent API, with agents
// a1 and a2 in it.
type turnRig struct {
	srv  *httptest.Server
	svc  *loomagent.Service
	wt   *agentworktree.Worktrees
	repo string
	git  gitrunner.Exec
}

func newTurnRig(t *testing.T, a1 loomstore.Agent) *turnRig {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	r := &turnRig{repo: filepath.Join(dir, "repo")}
	for _, args := range [][]string{{"init", "-q", "-b", "main", r.repo},
		{"-C", r.repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if _, err := r.git.Run(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	st, err := loomstore.Open(ctx, filepath.Join(dir, "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a2 := testAgent("a2", loomagent.StateIdle)
	a1.Repo, a2.Repo = r.repo, r.repo
	for _, a := range []loomstore.Agent{a1, a2} {
		if err := st.InsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if r.wt, err = agentworktree.New(filepath.Join(dir, "worktrees"), agentworktree.TargetLocal, r.git); err != nil {
		t.Fatal(err)
	}
	r.svc = loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: "ws", Workspace: agentworktree.Port{W: r.wt},
		Harnesses: map[string]loomharness.Harness{"fake": fake.New()}})
	mux := http.NewServeMux()
	New(func(string) *loomagent.Service { return r.svc }, nil).Register(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(middleware.WithWorkspace(req.Context(), req.PathValue("ws"))))
		})
	}, nil)
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

// turn writes files into id's working copy and captures it as turn n.
func (r *turnRig) turn(t *testing.T, id string, n int, files map[string]string) {
	t.Helper()
	ctx := context.Background()
	s := agentworktree.Spec{Key: id, Repo: r.repo, BaseRef: "main", Branch: "loom/agent/" + id}
	w, err := r.wt.Ensure(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(w.Path, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.wt.Checkpoint(ctx, s, "refs/loom/checkpoints/"+id+"/turn/"+strconv.Itoa(n)); err != nil {
		t.Fatal(err)
	}
}

// refs lists id's checkpoint refs in the repo.
func (r *turnRig) refs(t *testing.T, id string) []string {
	t.Helper()
	out, err := r.git.Run(context.Background(), r.repo, "for-each-ref", "--format=%(refname)", "refs/loom/checkpoints/"+id+"/")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(out)
}

// threeTurns gives id turns 0 (empty), 1 (writes a.txt) and 2 (edits a.txt, adds b.txt).
func (r *turnRig) threeTurns(t *testing.T, id string) {
	t.Helper()
	r.turn(t, id, 0, nil)
	r.turn(t, id, 1, map[string]string{"a.txt": "one\n"})
	r.turn(t, id, 2, map[string]string{"a.txt": "two\n", "b.txt": "bee\n"})
}

// TestTurnDiffApi: a turn's diff is exactly the files that turn changed,
// from the ref of the turn before (turn 1 from the turn/0 baseline).
func TestTurnDiffApi(t *testing.T) {
	r := newTurnRig(t, testAgent("a1", loomagent.StateIdle))
	r.threeTurns(t, "a1")
	for _, c := range []struct {
		n     string
		files []any
		patch []string
	}{
		{"1", []any{map[string]any{"path": "a.txt", "status": "added"}}, []string{"+one"}},
		{"2", []any{map[string]any{"path": "a.txt", "status": "modified"}, map[string]any{"path": "b.txt", "status": "added"}},
			[]string{"-one", "+two", "+bee"}},
	} {
		status, out := call(t, r.srv, "GET", "ws/v1/agents/a1/turns/"+c.n+"/diff", "", "")
		if status != 200 || !reflect.DeepEqual(out["files"], c.files) || out["turn"] != float64(c.n[0]-'0') {
			t.Fatalf("turn %s diff = %d %v; want files %v", c.n, status, out, c.files)
		}
		patch, _ := out["patch"].(string)
		for _, p := range c.patch {
			if !strings.Contains(patch, p) {
				t.Errorf("turn %s patch lacks %q:\n%s", c.n, p, patch)
			}
		}
		if c.n == "1" && (strings.Contains(patch, "two") || strings.Contains(patch, "bee")) {
			t.Errorf("turn 1 patch carries turn 2: %s", patch)
		}
	}
}

// TestTurnDiffUnknownTurn404: a turn with no checkpoint (not ended yet, or
// 0, the baseline) is 404 turn_not_found; an unknown agent is agent_not_found.
func TestTurnDiffUnknownTurn404(t *testing.T) {
	r := newTurnRig(t, testAgent("a1", loomagent.StateIdle))
	r.threeTurns(t, "a1")
	for _, path := range []string{"a1/turns/3/diff", "a1/turns/0/diff", "a2/turns/1/diff"} {
		if status, out := call(t, r.srv, "GET", "ws/v1/agents/"+path, "", ""); status != 404 || out["code"] != "turn_not_found" {
			t.Errorf("%s = %d %v; want 404 turn_not_found", path, status, out)
		}
	}
	if status, out := call(t, r.srv, "GET", "ws/v1/agents/nope/turns/1/diff", "", ""); status != 404 || out["code"] != "agent_not_found" {
		t.Errorf("unknown agent = %d %v; want 404 agent_not_found", status, out)
	}
	if status, _ := call(t, r.srv, "GET", "ws/v1/agents/a1/turns/x/diff", "", ""); status != 400 {
		t.Errorf("turn x = %d; want 400", status)
	}
}

// TestCheckpointRefsPurgedOnDelete: Delete removes every checkpoint ref of
// the agent and none of another agent's.
func TestCheckpointRefsPurgedOnDelete(t *testing.T) {
	r := newTurnRig(t, testAgent("a1", loomagent.StateIdle))
	r.threeTurns(t, "a1")
	r.turn(t, "a2", 0, nil)
	if status, out := call(t, r.srv, "DELETE", "ws/v1/agents/a1", "d1", ""); status != 204 {
		t.Fatalf("delete = %d %v", status, out)
	}
	if got := r.refs(t, "a1"); len(got) != 0 {
		t.Fatalf("a1 refs after delete: %v", got)
	}
	if got := r.refs(t, "a2"); len(got) != 1 {
		t.Fatalf("a2 refs after a1's delete: %v; want its turn/0", got)
	}
}

// TestCheckpointRefsPurgedOnHistoryPurge: the retention sweep that purges an
// agent's history removes its checkpoint refs with it; the diff is then
// history_expired. An agent not due keeps its refs.
func TestCheckpointRefsPurgedOnHistoryPurge(t *testing.T) {
	now := time.Now()
	a1 := testAgent("a1", loomagent.StateArchived)
	archived := loomstore.Stamp(now.Add(-loomstore.HistoryRetention - time.Hour))
	a1.ArchivedAt = &archived
	r := newTurnRig(t, a1)
	r.threeTurns(t, "a1")
	r.turn(t, "a2", 0, nil)
	r.svc.RetentionSweep(context.Background(), now)
	if got := r.refs(t, "a1"); len(got) != 0 {
		t.Fatalf("a1 refs after the history purge: %v", got)
	}
	if got := r.refs(t, "a2"); len(got) != 1 {
		t.Fatalf("a2 refs after a1's purge: %v; want its turn/0", got)
	}
	if status, out := call(t, r.srv, "GET", "ws/v1/agents/a1/turns/1/diff", "", ""); status != 410 || out["code"] != "history_expired" {
		t.Fatalf("diff after purge = %d %v; want 410 history_expired", status, out)
	}
}
