//go:build daemon_bugreplay && unix

package lockfile_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/configlock"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// Child processes use only paths supplied by the parent under t.TempDir.
func TestLockBugReplayChild(t *testing.T) {
	mode := os.Getenv("LOOM_LOCK_REPLAY_CHILD")
	if mode == "" {
		return
	}
	path := os.Getenv("LOOM_LOCK_REPLAY_PATH")
	switch mode {
	case "flock":
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fmt.Println("OPEN")
		r := bufio.NewReader(os.Stdin)
		if _, err := r.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		err = lockfile.TryLockExclusive(f)
		if errors.Is(err, lockfile.ErrLocked) {
			fmt.Println("BUSY")
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("HELD")
		if _, err := r.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("LOOM_LOCK_REPLAY_UNLINK") == "1" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		if err := lockfile.FlockUnlock(f); err != nil {
			t.Fatal(err)
		}
	case "config":
		for i := 0; i < 10; i++ {
			err := configlock.WithLock(filepath.Dir(path), func() error {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				n, err := strconv.Atoi(strings.TrimSpace(string(data)))
				if err != nil {
					return err
				}
				return os.WriteFile(path, []byte(strconv.Itoa(n+1)), 0600)
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

type replayProcess struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Scanner
}

func startReplayProcess(t *testing.T, mode, path string, unlink bool) *replayProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockBugReplayChild$")
	cmd.Env = append(os.Environ(), "LOOM_LOCK_REPLAY_CHILD="+mode, "LOOM_LOCK_REPLAY_PATH="+path)
	if unlink {
		cmd.Env = append(cmd.Env, "LOOM_LOCK_REPLAY_UNLINK=1")
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &replayProcess{cmd: cmd, in: in, out: bufio.NewScanner(out)}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return p
}

func expectLine(t *testing.T, p *replayProcess, want string) {
	t.Helper()
	if !p.out.Scan() {
		t.Fatalf("child output ended before %q: %v", want, p.out.Err())
	}
	if got := p.out.Text(); got != want {
		t.Fatalf("child output %q, want %q", got, want)
	}
}

func sendLine(t *testing.T, p *replayProcess) {
	t.Helper()
	if _, err := io.WriteString(p.in, "go\n"); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonLockInodeHandoff(t *testing.T) {
	for _, tc := range []struct {
		name   string
		legacy bool
		third  string
	}{
		{"stable-path", false, "BUSY"}, {"unlink-while-held-control", true, "HELD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "daemon.lock")
			a := startReplayProcess(t, "flock", path, tc.legacy)
			expectLine(t, a, "OPEN")
			sendLine(t, a)
			expectLine(t, a, "HELD")
			b := startReplayProcess(t, "flock", path, false)
			expectLine(t, b, "OPEN") // B keeps the old inode open across A's release.
			sendLine(t, a)
			if err := a.cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			sendLine(t, b)
			expectLine(t, b, "HELD")
			c := startReplayProcess(t, "flock", path, false)
			expectLine(t, c, "OPEN")
			sendLine(t, c)
			expectLine(t, c, tc.third)
			if tc.third == "HELD" {
				sendLine(t, c)
			}
			if err := c.cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			sendLine(t, b)
			if err := b.cmd.Wait(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConfigLockMultiProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counter")
	if err := os.WriteFile(path, []byte("0"), 0600); err != nil {
		t.Fatal(err)
	}
	var children []*replayProcess
	for i := 0; i < 4; i++ {
		children = append(children, startReplayProcess(t, "config", path, false))
	}
	for _, p := range children {
		if err := p.cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "40" {
		t.Fatalf("config lock lost writes: counter=%s, want 40", data)
	}
}
