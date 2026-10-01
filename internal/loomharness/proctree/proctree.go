// Package proctree reaps the process tree a harness adapter started. Harness
// servers start tool commands that leave their process group (OpenCode's
// shell commands and PTY daemon, codex's exec sessions), so they outlive a
// crashed or stopped server. A Tree records the descendants of a server while
// it runs, by parent PID, with their start times; Reap signals exactly the
// recorded processes that still run with the same start time, plus their
// current descendants. It never pattern-kills and never touches a process
// outside the recorded tree, such as the user's own harness.
package proctree

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Tree is the recorded process tree of one supervisor.
type Tree struct {
	mu   sync.Mutex
	tree map[int]string // recorded descendant PID -> start time
}

// New returns an empty tree.
func New() *Tree { return &Tree{tree: map[int]string{}} }

// Track records root's descendants every interval until exited is closed.
func (t *Tree) Track(root int, exited <-chan struct{}, every time.Duration) {
	for {
		t.Record(root)
		select {
		case <-exited:
			return
		case <-time.After(every):
		}
	}
}

// Record adds the current descendants of root to the tree.
func (t *Tree) Record(root int) {
	procs := processes()
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, pid := range descendants(procs, []int{root}) {
		t.tree[pid] = procs[pid].start
	}
}

// Owned lists the recorded processes that still run with the same start
// time, plus their current descendants, and forgets the rest.
func (t *Tree) Owned() []int {
	procs := processes()
	t.mu.Lock()
	defer t.mu.Unlock()
	var roots []int
	for pid, start := range t.tree {
		if p, ok := procs[pid]; ok && p.start == start {
			roots = append(roots, pid)
		} else {
			delete(t.tree, pid)
		}
	}
	return append(roots, descendants(procs, roots)...)
}

// Reap stops the owned processes: SIGTERM, then SIGKILL after grace. It lists
// them again before each signal, so an exited, reused PID is never hit.
func (t *Tree) Reap(grace time.Duration) {
	sig := syscall.SIGTERM
	for deadline := time.Now().Add(grace); ; time.Sleep(50 * time.Millisecond) {
		pids := t.Owned()
		if len(pids) == 0 {
			return
		}
		if time.Now().After(deadline) {
			sig = syscall.SIGKILL
		}
		for _, pid := range pids {
			_ = syscall.Kill(pid, sig)
		}
		if sig == syscall.SIGKILL {
			return
		}
	}
}

type process struct {
	ppid  int
	start string
}

// processes is the ps table: PID -> parent and start time. Zombies are left
// out; they are already dead.
func processes() map[int]process {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,stat=,lstart=").Output()
	if err != nil {
		return nil
	}
	procs := map[int]process{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || strings.HasPrefix(f[2], "Z") {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			procs[pid] = process{ppid: ppid, start: strings.Join(f[3:], " ")}
		}
	}
	return procs
}

// descendants lists every process below roots in procs, never Loom itself.
func descendants(procs map[int]process, roots []int) []int {
	kids := map[int][]int{}
	for pid, p := range procs {
		kids[p.ppid] = append(kids[p.ppid], pid)
	}
	var out []int
	for queue := slices.Clone(roots); len(queue) > 0; {
		pid := queue[0]
		queue = queue[1:]
		for _, k := range kids[pid] {
			if k != os.Getpid() {
				out = append(out, k)
				queue = append(queue, k)
			}
		}
	}
	return out
}
