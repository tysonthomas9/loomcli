package proctree

import (
	"os/exec"
	"sync"
	"syscall"
)

// Group is a started process-group leader (Setpgid) and its group. The group
// is signaled only while the leader is unreaped: until then the kernel
// cannot reuse its PID as a process or group id, so a signal never reaches
// an unrelated group.
type Group struct {
	cmd    *exec.Cmd
	mu     sync.Mutex
	reaped bool
}

// NewGroup wraps a started command that leads its own process group.
func NewGroup(cmd *exec.Cmd) *Group { return &Group{cmd: cmd} }

// Pid is the leader's PID, which is also the group id.
func (g *Group) Pid() int { return g.cmd.Process.Pid }

// Kill SIGKILLs the whole group, unless the leader is already reaped.
func (g *Group) Kill() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.reaped {
		_ = syscall.Kill(-g.Pid(), syscall.SIGKILL)
	}
}

// Wait blocks until the leader exits, SIGKILLs what is left of its group
// while the leader is still unreaped, then reaps it.
func (g *Group) Wait() error {
	_ = exited(g.Pid())
	g.mu.Lock()
	defer g.mu.Unlock()
	_ = syscall.Kill(-g.Pid(), syscall.SIGKILL)
	g.reaped = true
	return g.cmd.Wait()
}
