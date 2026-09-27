//go:build daemon_bugreplay

// Bug-replay fault tests (restart group, config/boot class) for daemon boot.
// See internal/cli/daemon/supervisor/bugreplay_restart_test.go.
package daemon

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// #443: one agent with an unresolvable role (or worktree) aborts supervisor
// init, so no agent in the workspace is supervised.
// Root cause: daemon.go:320-323 initSupervisorAgents returns on the first
// error. The result is discarded with `_ =` because the fix changes its type
// (error -> []UnavailableAgent); the observable is sup.Agents.
func TestBugReplay_PR443_OneBadAgentDoesNotAbortBoot(t *testing.T) {
	cfg := &cfgpkg.DaemonConfig{}
	sup := &supervisor.Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg },
		FindRepoConfig: func(string) *cfgpkg.RepoConfig { return nil },
	}
	agents := []cfgpkg.AgentEntry{
		{Worktree: t.TempDir(), Role: "replay-no-such-role"}, // broken definition first
		{Worktree: t.TempDir(), Role: "task"},                // healthy sibling
	}
	_ = initSupervisorAgents(sup, agents, cfg.Roles)
	if len(sup.Agents) != 1 || sup.Agents[0].Entry.Role != "task" {
		t.Fatalf("supervised agents = %d; the healthy agent was dropped because a sibling's role is unresolvable", len(sup.Agents))
	}
}

// #535: a daemon started from inside an agent session inherits that agent's
// identity env (LOOM_AGENT_NAME, LOOM_ASSIGNED_TASK_ID) and boots anyway.
// Root cause: daemon_cmd.go:218 runDaemonBody never checks the env (and
// control_plane.go:157 then reads LOOM_AGENT_NAME as the node owner).
// runDaemonBody isolates its process group, so it runs in a child process.
func TestBugReplay_PR535_RefuseBootWithInheritedAgentIdentity(t *testing.T) {
	if os.Getenv("BUGREPLAY_RESTART_535_CHILD") == "1" {
		os.Exit(runDaemonBody())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBugReplay_PR535_RefuseBootWithInheritedAgentIdentity$")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"BUGREPLAY_RESTART_535_CHILD=1",
		"LOOM_AGENT_NAME=replay-worker",
		"LOOM_ASSIGNED_TASK_ID=loom-535",
		"LOOM_CONFIG_DIR="+t.TempDir(),
		"LOOM_WORKSPACE=WS535",
		"LOOM_FLEET_DB_URL=http://127.0.0.1:1", // any config load fails fast, never dials a real fleet-db
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatal("daemon child did not exit within 30s")
	}
	if err == nil {
		t.Fatal("daemon child exited 0 with an inherited agent identity")
	}
	if !strings.Contains(stderr.String(), "agent identity") {
		t.Fatalf("daemon did not refuse the inherited agent identity; it went on to boot and failed later:\n%s", stderr.String())
	}
}
