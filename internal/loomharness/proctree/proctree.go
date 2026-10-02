// Package proctree reaps the process tree a harness adapter started. Harness
// servers start tool commands that leave their process group (OpenCode's
// shell commands and PTY daemon, codex's exec sessions), so they outlive a
// crashed or stopped server. A Tree records the descendants of a server while
// it runs, by parent PID, with their exact kernel start times; Reap signals
// exactly the recorded processes that still run with the same start time,
// plus their current descendants, and checks the start time again right
// before each signal. It never pattern-kills and never touches a process
// outside the recorded tree, such as the user's own harness.
//
// A process is recorded when Track samples it while it descends from the
// server. One that is started and orphaned (its parent exits) between two
// samples is reparented away first and is not recorded, so it is not reaped.
package proctree

import (
	"os"
	"slices"
	"sync"
	"syscall"
	"time"
)

// Tree is the recorded process tree of one supervisor.
type Tree struct {
	mu   sync.Mutex
	tree map[int]int64 // recorded descendant PID -> start time
}

// New returns an empty tree.
func New() *Tree { return &Tree{tree: map[int]int64{}} }

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
	var pids []int
	for pid := range t.owned() {
		pids = append(pids, pid)
	}
	return pids
}

// owned is Owned with each process's start time.
func (t *Tree) owned() map[int]int64 {
	procs := processes()
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[int]int64{}
	var roots []int
	for pid, start := range t.tree {
		if p, ok := procs[pid]; ok && p.start == start {
			roots = append(roots, pid)
			out[pid] = start
		} else {
			delete(t.tree, pid)
		}
	}
	for _, pid := range descendants(procs, roots) {
		out[pid] = procs[pid].start
	}
	return out
}

// Reap stops the owned processes: SIGTERM, then SIGKILL after grace. It
// lists them again before each round and re-checks each start time right
// before its signal, so a PID reused by another process is not hit.
func (t *Tree) Reap(grace time.Duration) {
	sig := syscall.SIGTERM
	for deadline := time.Now().Add(grace); ; time.Sleep(50 * time.Millisecond) {
		owned := t.owned()
		if len(owned) == 0 {
			return
		}
		if time.Now().After(deadline) {
			sig = syscall.SIGKILL
		}
		for pid, start := range owned {
			if now, ok := startOf(pid); ok && now == start {
				_ = syscall.Kill(pid, sig)
			}
		}
		if sig == syscall.SIGKILL {
			return
		}
	}
}

type process struct {
	ppid  int
	start int64 // exact kernel start time
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
