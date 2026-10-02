package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCodexNestedLaunchStripsGitHubTokens runs the real codex app-server for
// the shared inherited root and a profile root, both owned /tmp fixtures,
// from an environment seeded with every GitHub token. Neither the app-server
// process nor a nested subprocess it runs (command/exec) receives any token;
// a marker variable still passes. It also checks, on the real server, that
// concurrent calls each get their own result.
func TestCodexNestedLaunchStripsGitHubTokens(t *testing.T) {
	if os.Getenv("LOOM_REAL_CODEX") != "1" {
		t.Skip("set LOOM_REAL_CODEX=1 to run against the installed codex")
	}
	sbx, err := os.MkdirTemp("/tmp", "loom-codex-nested-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sbx) })
	inherited, profile := filepath.Join(sbx, "inherited"), filepath.Join(sbx, "profile")
	for _, d := range []string{filepath.Join(sbx, "home"), inherited, profile} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(sbx, "home"), "CODEX_HOME=" + inherited, "LOOM_MARK=kept"}
	for _, k := range githubTokens {
		env = append(env, k+"=synthetic-"+strings.ToLower(k))
	}
	bin := os.Getenv("LOOM_CODEX_BIN")
	s := New(Config{Bin: bin, Env: env})
	t.Cleanup(s.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pids := map[int]bool{}
	for _, root := range []string{"", profile} {
		c, err := s.Conn(ctx, root)
		if err != nil {
			t.Fatalf("root %q: %v", root, err)
		}
		srv := s.servers[s.Root(root)]
		pids[srv.group.Pid()] = true
		assertEnvNames(t, "app-server for "+s.Root(root), processEnv(t, srv.group.Pid()))
		var res struct {
			ExitCode int
			Stdout   string
		}
		if err := c.Call(ctx, "command/exec", map[string]any{"command": []string{"/usr/bin/env"}, "cwd": sbx}, &res); err != nil || res.ExitCode != 0 {
			t.Fatalf("command/exec env: %v (exit %d)", err, res.ExitCode)
		}
		assertEnvNames(t, "nested command under "+s.Root(root), "\n"+res.Stdout)
		concurrentEcho(ctx, t, c)
	}
	if len(pids) != 2 {
		t.Fatalf("want one app-server per root, got pids %v", pids)
	}
}

// processEnv is the environment of a same-user process as `ps -E` shows it.
func processEnv(t *testing.T, pid int) string {
	t.Helper()
	out, err := exec.Command("ps", "-E", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatal(err)
	}
	return " " + strings.ReplaceAll(string(out), "\n", " ")
}

// assertEnvNames checks names only, so values never reach test output.
func assertEnvNames(t *testing.T, what, env string) {
	t.Helper()
	sep := " "
	if strings.HasPrefix(env, "\n") {
		sep = "\n"
	}
	for _, k := range githubTokens {
		if strings.Contains(env, sep+k+"=") {
			t.Errorf("%s has %s", what, k)
		}
	}
	if !strings.Contains(env, sep+"LOOM_MARK=kept") {
		t.Errorf("%s lost LOOM_MARK", what)
	}
}

func concurrentEcho(ctx context.Context, t *testing.T, c *Conn) {
	t.Helper()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			var res struct{ Stdout string }
			want := "echo-" + strconv.Itoa(i)
			if err := c.Call(ctx, "command/exec", map[string]any{"command": []string{"/bin/echo", want}}, &res); err != nil || strings.TrimSpace(res.Stdout) != want {
				t.Errorf("call %d got %q: %v", i, res.Stdout, err)
			}
		})
	}
	wg.Wait()
}
