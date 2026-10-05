package agentwire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentworktree"
	"github.com/tysonthomas9/loomcli/internal/gitrunner"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/opencode"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// TestDeleteIdleOpenCodeAgentReal (DEL1): with the service serve wires and
// the pinned OpenCode build, deleting an idle agent reaches the tombstone,
// both through a live Delete and when a restarted Loom finds a Delete that
// failed after it removed the working copy. OpenCode never loaded the
// agent's location, so once the Delete removes the working copy OpenCode
// answers its Retire (Unbridge) with 500; that used to fail every retry and
// leave the agent stopping.
// LOOM_REAL_OPENCODE=1 enables it; LOOM_OPENCODE_BIN overrides the binary.
func TestDeleteIdleOpenCodeAgentReal(t *testing.T) {
	if os.Getenv("LOOM_REAL_OPENCODE") != "1" {
		t.Skip("set LOOM_REAL_OPENCODE=1 to run against the real OpenCode build")
	}
	bin := os.Getenv("LOOM_OPENCODE_BIN")
	if bin == "" {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".loom/harness/opencode/2.0.19/opencode")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull) // no host git config: hooks, signing or includes
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sbx, env := openCodeSandbox(t)
	repo := filepath.Join(sbx, "repo")
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	oc := opencode.New(opencode.Config{Bin: bin, Env: env})
	t.Cleanup(oc.Stop)
	if err := oc.Restart(ctx); err != nil { // connects, starting the sandbox's service, as serve's first agent call does
		t.Fatal(err)
	}
	st, err := loomstore.Open(ctx, filepath.Join(sbx, "agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	wt, err := agentworktree.New(filepath.Join(sbx, "worktrees"), agentworktree.TargetLocal, gitrunner.Exec{})
	if err != nil {
		t.Fatal(err)
	}
	service := func(r func(context.Context, loomstore.Agent) error) *loomagent.Service {
		c := serviceConfig(st, "ws", wt, nil, map[string]loomharness.Harness{"opencode": oc})
		c.Retire = r
		return loomagent.New(c)
	}
	insert := func(id, path string) {
		t.Helper()
		base, branch := "main", "loom/agent/"+id
		row := loomstore.Agent{AgentID: id, WorkspaceID: "ws", Name: id, ProfileKey: id, Preset: "lead", PresetVersion: "1",
			Mode: "persistent", InteractionMode: "interactive", RoleKind: "interactive", SpecJSON: "{}", SpecVersion: 1,
			OwnerKind: "user", OwnerID: "u", CreatedByKind: "user", CreatedByID: "u", CreateRequestID: "r-" + id,
			Repo: repo, BaseRef: &base, Branch: &branch, WorktreePath: &path, Harness: "opencode", State: loomagent.StateIdle, CreateStep: 5}
		if err := st.InsertAgent(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	deleted := func(id string) bool {
		row, err := st.GetAgent(ctx, id)
		return err == nil && row.DeletedAt != nil
	}

	worktree := func(id string) string {
		t.Helper()
		w, err := wt.Ensure(ctx, agentworktree.Spec{Key: id, Repo: repo, BaseRef: "main", Branch: "loom/agent/" + id})
		if err != nil {
			t.Fatal(err)
		}
		insert(id, w.Path)
		return w.Path
	}

	t.Run("live", func(t *testing.T) {
		path := worktree("live")
		if err := service(retire(oc)).Delete(ctx, loomagent.DeleteRequest{AgentID: "live"}); err != nil || !deleted("live") {
			t.Fatalf("Delete = %v, tombstoned %t; want nil and the tombstone", err, deleted("live"))
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("working copy %s still there: %v", path, err)
		}
	})
	t.Run("after restart", func(t *testing.T) {
		path := worktree("restart")
		down := func(context.Context, loomstore.Agent) error { return errors.New("loom stopped") } // Loom stopped after the working copy went
		if err := service(down).Delete(ctx, loomagent.DeleteRequest{AgentID: "restart"}); err == nil {
			t.Fatal("Delete succeeded although Retire failed")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) || deleted("restart") {
			t.Fatalf("before the restart: working copy %v, tombstoned %t; want it removed and not tombstoned", err, deleted("restart"))
		}
		dctx, stop := context.WithCancel(ctx)
		defer stop()
		svc := service(retire(oc)) // the restarted Loom: its dispatcher's start-up resync retries the Delete
		go svc.Dispatcher()(dctx)
		if err := svc.Drain(ctx); err != nil {
			t.Fatal(err)
		}
		if row, _ := st.GetAgent(ctx, "restart"); row.DeletedAt == nil {
			t.Fatalf("after the restart: state %s, Attention %q; want the tombstone", row.State, attention(row))
		}
	})
}

// openCodeSandbox makes a /tmp sandbox for one OpenCode service: its own
// HOME, TMPDIR and XDG roots, an empty user config, a free loopback port
// and a repo dir, and the environment to run OpenCode there with nothing
// from the host but what a shell needs. At cleanup it stops the service
// registered there (Loom leaves the one it starts running) and removes it.
func openCodeSandbox(t *testing.T) (string, []string) {
	t.Helper()
	sbx, err := os.MkdirTemp("/tmp", "loom-del1-")
	if err == nil {
		sbx, err = filepath.EvalSymlinks(sbx)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var reg struct {
			URL, Password string
			PID           int
		}
		if b, err := os.ReadFile(filepath.Join(sbx, "state/opencode/service.json")); err == nil && json.Unmarshal(b, &reg) == nil &&
			reg.PID > 0 && servicePID(reg.URL, reg.Password) == reg.PID { // the sandbox's service, not a reused pid
			_ = syscall.Kill(reg.PID, syscall.SIGTERM)
			for i := 0; i < 100 && syscall.Kill(reg.PID, 0) == nil; i++ {
				time.Sleep(100 * time.Millisecond)
			}
			_ = syscall.Kill(reg.PID, syscall.SIGKILL)
		}
		_ = os.RemoveAll(sbx)
	})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	cfg := filepath.Join(sbx, "config/opencode")
	for _, d := range []string{filepath.Join(sbx, "home"), filepath.Join(sbx, "tmp"), cfg, filepath.Join(sbx, "repo")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{"opencode.json": "{}", "service.json": fmt.Sprintf(`{"port":%d}`, port)} {
		if err := os.WriteFile(filepath.Join(cfg, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"OPENCODE_DISABLE_MODELS_FETCH=1", "HOME=" + filepath.Join(sbx, "home"), "TMPDIR=" + filepath.Join(sbx, "tmp") + "/",
		"XDG_DATA_HOME=" + filepath.Join(sbx, "data"), "XDG_CONFIG_HOME=" + filepath.Join(sbx, "config"),
		"XDG_STATE_HOME=" + filepath.Join(sbx, "state"), "XDG_CACHE_HOME=" + filepath.Join(sbx, "cache")}
	for _, k := range []string{"PATH", "SHELL", "LANG", "USER", "LOGNAME"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return sbx, env
}

func attention(a loomstore.Agent) string {
	if a.AttentionReason == nil {
		return ""
	}
	return *a.AttentionReason
}

// servicePID is the pid the OpenCode service at base reports, or 0.
func servicePID(base, password string) int {
	req, err := http.NewRequest("GET", base+"/api/info", nil)
	if err != nil {
		return 0
	}
	req.SetBasicAuth("opencode", password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var info struct {
		PID int `json:"pid"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&info) != nil {
		return 0
	}
	return info.PID
}
