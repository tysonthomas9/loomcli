//go:build daemon_bugreplay

// Bug-replay fault tests (restart group, config/boot class) for daemon config
// loading. See internal/cli/daemon/supervisor/bugreplay_restart_test.go.
package config

import (
	"context"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// #493: when more runnable agents exist than max_agents, validateAgents
// rejects the WHOLE daemon config, so no agent runs at all.
// Root cause: project.go:408-423 validateAgents errors on runnable > max.
func TestBugReplay_PR493_MaxAgentsDoesNotPoisonConfigLoad(t *testing.T) {
	const wsKey = "WS493"
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: wsKey, Name: wsKey, DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: wsKey, Name: "worker", Kind: "worker", PromptFile: "worker.md"}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: wsKey, Name: fmt.Sprintf("agent%d", i), RoleName: "worker"}); err != nil {
			t.Fatal(err)
		}
	}
	dc := newDefaultDaemonConfig()
	dc.Daemon.MaxAgents = IntPtr(2)

	got, err := loadDaemonConfigFromStore(ctx, st, wsKey, dc, t.TempDir())
	if err != nil {
		t.Fatalf("3 agents with max_agents=2 rejected the whole daemon config: %v", err)
	}
	if len(got.Agents) != 3 {
		t.Fatalf("loaded %d agents, want all 3 (max_agents caps what runs, not what loads)", len(got.Agents))
	}
}
