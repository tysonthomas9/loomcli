package proctree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain lets the test binary play a harness server: LOOM_PROCTREE=server
// starts a detached grandchild (its own session, so it leaves the server's
// process group), writes the grandchild's pid to LOOM_PROCTREE_OUT and
// sleeps; =leader starts a child in its own process group, writes its pid
// and exits; =sleep only sleeps.
func TestMain(m *testing.M) {
	switch os.Getenv("LOOM_PROCTREE") {
	case "leader":
		p, err := start("sleep", false)
		if err != nil {
			os.Exit(1)
		}
		_ = os.WriteFile(os.Getenv("LOOM_PROCTREE_OUT"), []byte(fmt.Sprint(p.Pid)), 0o600)
		os.Exit(0)
	case "server":
		p, err := start("sleep", true)
		if err != nil {
			os.Exit(1)
		}
		_ = os.WriteFile(os.Getenv("LOOM_PROCTREE_OUT"), []byte(fmt.Sprint(p.Pid)), 0o600)
		time.Sleep(time.Hour)
	case "sleep":
		time.Sleep(time.Hour)
	}
	os.Exit(m.Run())
}

func start(mode string, setsid bool) (*os.Process, error) {
	env := append(slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "LOOM_PROCTREE=") }), "LOOM_PROCTREE="+mode)
	return os.StartProcess(os.Args[0], os.Args[:1], &os.ProcAttr{Env: env, Sys: &syscall.SysProcAttr{Setsid: setsid}})
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// TestReapDetachedDescendant: a server's detached grandchild, recorded while
// the server ran, is reaped after the server dies; an unrelated process is
// left alone.
func TestReapDetachedDescendant(t *testing.T) {
	out := filepath.Join(t.TempDir(), "child")
	t.Setenv("LOOM_PROCTREE_OUT", out)
	server, err := start("server", false)
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := start("sleep", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unrelated.Kill(); _, _ = unrelated.Wait() })

	var child int
	for deadline := time.Now().Add(10 * time.Second); child == 0; time.Sleep(20 * time.Millisecond) {
		b, _ := os.ReadFile(out)
		_, _ = fmt.Sscan(string(b), &child)
		if time.Now().After(deadline) {
			t.Fatal("server never started its grandchild")
		}
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })

	tree := New()
	exited := make(chan struct{})
	go tree.Track(server.Pid, exited, 10*time.Millisecond)
	for deadline := time.Now().Add(5 * time.Second); !slices.Contains(tree.Owned(), child); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d never recorded", child)
		}
	}
	_ = server.Kill() // a crash: the grandchild survives, reparented
	_, _ = server.Wait()
	close(exited)
	if !alive(child) {
		t.Fatal("the detached grandchild died with the server; the test proves nothing")
	}

	tree.Reap(time.Second)
	for deadline := time.Now().Add(5 * time.Second); alive(child); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d survived Reap", child)
		}
	}
	if !alive(unrelated.Pid) {
		t.Fatal("Reap killed an unrelated process")
	}
	if owned := tree.Owned(); len(owned) != 0 {
		t.Fatalf("a reaped tree still owns %v", owned)
	}
}

// TestReapChecksStartTime: a recorded PID whose process has another start
// time (the PID was reused) is never signaled.
func TestReapChecksStartTime(t *testing.T) {
	other, err := start("sleep", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Kill(); _, _ = other.Wait() })
	start, ok := startOf(other.Pid)
	if !ok {
		t.Fatal("no start time for a running process")
	}
	tree := New()
	tree.tree[other.Pid] = start + 1 // the recorded process, one microsecond (or tick) older
	tree.Reap(100 * time.Millisecond)
	if !alive(other.Pid) {
		t.Fatal("Reap signaled a process with another start time")
	}
	tree.tree[other.Pid] = start
	if owned := tree.Owned(); !slices.Equal(owned, []int{other.Pid}) {
		t.Fatalf("exact start time not owned: %v", owned)
	}
}

// TestGroupWaitKillsGroupBeforeReap: when a group leader exits on its own,
// Wait kills the rest of its process group (while the leader is unreaped),
// and Kill after Wait signals nothing.
func TestGroupWaitKillsGroupBeforeReap(t *testing.T) {
	out := filepath.Join(t.TempDir(), "child")
	cmd := exec.Command(os.Args[0]) //nolint:norawexec // re-runs this test binary as the group leader
	cmd.Env = append(os.Environ(), "LOOM_PROCTREE=leader", "LOOM_PROCTREE_OUT="+out)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	g := NewGroup(cmd)
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
	var child int
	b, _ := os.ReadFile(out)
	if _, err := fmt.Sscan(string(b), &child); err != nil {
		t.Fatal("the leader never started its child")
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	for deadline := time.Now().Add(5 * time.Second); alive(child); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("group member %d survived its leader's Wait", child)
		}
	}
	g.Kill() // a no-op: the group id may already belong to someone else
}
