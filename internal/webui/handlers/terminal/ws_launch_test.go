package terminal

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func newTabMetaStoreForWSTest(t *testing.T) *tabmeta.Store {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return tabmeta.NewStore(rdb, nil)
}

func TestLaunchSpecRejectsUUIDSessionWithoutMetadata(t *testing.T) {
	ctx := context.Background()
	p := &terminalWSParams{tabMetaStore: newTabMetaStoreForWSTest(t)}

	launch, err := launchSpecForTerminalSession(ctx, p, "E2E", "term_550e8400-e29b-41d4-a716-446655440000")
	if launch != nil {
		t.Fatalf("launch = %#v, want nil", launch)
	}
	if !errors.Is(err, errTerminalLaunchMetaMissing) {
		t.Fatalf("err = %v, want errTerminalLaunchMetaMissing", err)
	}
}

func TestLaunchSpecRejectsUUIDSessionWithoutTabStore(t *testing.T) {
	ctx := context.Background()
	p := &terminalWSParams{}

	launch, err := launchSpecForTerminalSession(ctx, p, "E2E", "term_550e8400-e29b-41d4-a716-446655440000")
	if launch != nil {
		t.Fatalf("launch = %#v, want nil", launch)
	}
	if !errors.Is(err, errTerminalLaunchMetaMissing) {
		t.Fatalf("err = %v, want errTerminalLaunchMetaMissing", err)
	}
}

func TestLaunchSpecKeepsLegacyNamedLeadTabs(t *testing.T) {
	ctx := context.Background()
	p := &terminalWSParams{tabMetaStore: newTabMetaStoreForWSTest(t)}

	launch, err := launchSpecForTerminalSession(ctx, p, "E2E", "lead-codex-1")
	if err != nil {
		t.Fatalf("launchSpecForTerminalSession: %v", err)
	}
	if launch == nil || len(launch.Argv) == 0 {
		t.Fatalf("launch = %#v, want legacy lead argv", launch)
	}
}

func TestEnsureWorkspacePTYRegisteredUsesLocalState(t *testing.T) {
	stateDir := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", stateDir)
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Version: 1,
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"E2E": {Path: wsDir},
		},
	}); err != nil {
		t.Fatalf("SaveStateCache: %v", err)
	}

	mm := webuterminal.NewMultiPTYManager("cat", 0)
	t.Cleanup(func() { _ = mm.Close() })
	p := &terminalWSParams{manager: mm}

	ensureWorkspacePTYRegistered(context.Background(), p, "E2E")

	_, _, err := mm.AttachSession(webuterminal.SessionKey{Workspace: "E2E", Name: "s"}, 80, 24, &webuterminal.LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("AttachSession after self-heal: %v", err)
	}
}
