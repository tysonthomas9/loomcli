package proctree

import (
	"fmt"
	"os"
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
// sleeps; =sleep only sleeps.
func TestMain(m *testing.M) {
	switch os.Getenv("LOOM_PROCTREE") {
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
