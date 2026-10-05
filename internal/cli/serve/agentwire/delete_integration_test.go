package agentwire

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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
// both through a live Delete and when a restarted Loom finds the Delete left
// half done (stopping, flagged, Attention delete_incomplete, its working
// copy gone). OpenCode never loaded the agent's location, so once the
// Delete removes the working copy OpenCode answers its Retire (Unbridge)
// with 500; that used to fail every retry and leave the agent stopping.
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
	service := func() *loomagent.Service {
		c := serviceConfig(st, "ws", wt, nil, map[string]loomharness.Harness{"opencode": oc})
		c.Retire = retire(oc)
		return loomagent.New(c)
	}
	insert := func(id, state, path string, flagged bool) {
		t.Helper()
		base, branch := "main", "loom/agent/"+id
		row := loomstore.Agent{AgentID: id, WorkspaceID: "ws", Name: id, ProfileKey: id, Preset: "lead", PresetVersion: "1",
			Mode: "persistent", InteractionMode: "interactive", RoleKind: "interactive", SpecJSON: "{}", SpecVersion: 1,
			OwnerKind: "user", OwnerID: "u", CreatedByKind: "user", CreatedByID: "u", CreateRequestID: "r-" + id,
			Repo: repo, BaseRef: &base, Branch: &branch, WorktreePath: &path, Harness: "opencode", State: state, CreateStep: 5}
		if flagged {
			reason := loomagent.AttentionDeleteIncomplete
			row.DeleteRequested, row.AttentionReason = true, &reason
		}
		if err := st.InsertAgent(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	deleted := func(id string) bool {
		row, err := st.GetAgent(ctx, id)
		return err == nil && row.DeletedAt != nil
	}

	t.Run("live", func(t *testing.T) {
		w, err := wt.Ensure(ctx, agentworktree.Spec{Key: "live", Repo: repo, BaseRef: "main", Branch: "loom/agent/live"})
		if err != nil {
			t.Fatal(err)
		}
		insert("live", loomagent.StateIdle, w.Path, false)
		if err := service().Delete(ctx, loomagent.DeleteRequest{AgentID: "live"}); err != nil || !deleted("live") {
			t.Fatalf("Delete = %v, tombstoned %t; want nil and the tombstone", err, deleted("live"))
		}
		if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
			t.Fatalf("working copy %s still there: %v", w.Path, err)
		}
	})
	t.Run("after restart", func(t *testing.T) {
		insert("restart", loomagent.StateStopping, filepath.Join(sbx, "worktrees", "repo", "restart"), true)
		dctx, stop := context.WithCancel(ctx)
		defer stop()
		go service().RunDispatcher(dctx) // a restarted Loom: its start-up resync retries the Delete
		for deadline := time.Now().Add(30 * time.Second); !deleted("restart"); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				row, _ := st.GetAgent(ctx, "restart")
				t.Fatalf("after 30s: state %s, Attention %q; want the tombstone", row.State, attention(row))
			}
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
			PID int `json:"pid"`
		}
		if b, err := os.ReadFile(filepath.Join(sbx, "state/opencode/service.json")); err == nil && json.Unmarshal(b, &reg) == nil && reg.PID > 0 {
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
