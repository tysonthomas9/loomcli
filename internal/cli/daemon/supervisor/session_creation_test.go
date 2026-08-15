package supervisor

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

func TestCreateAgentSession_ResolvesDefaultBackend(t *testing.T) {
	backendFlag := cli.TestingBackendFlag()
	originalBackendFlag := *backendFlag
	*backendFlag = ""
	t.Cleanup(func() { *backendFlag = originalBackendFlag })

	tests := []struct {
		name        string
		envBackend  string
		wantBackend string
	}{
		{name: "default", wantBackend: backendnames.Codex},
		{name: "environment override", envBackend: "opencode", wantBackend: "opencode"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOOM_BACKEND", tt.envBackend)
			runtimeDir := t.TempDir()
			t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
			cli.ResetWorkspaceRuntimeDirCache()
			t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

			s := &Supervisor{
				ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} },
			}
			ap := &AgentProcess{
				Entry:        cfgpkg.AgentEntry{Worktree: "worker"},
				RoleConfig:   cfgpkg.RoleConfig{},
				WorktreePath: t.TempDir(),
			}

			s.createAgentSession(ap, "")
			if ap.AgentSessionID == "" {
				t.Fatal("createAgentSession did not create a session")
			}

			store, err := sessions.NewStore(runtimeDir)
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			meta, err := store.LoadMetadata(ap.AgentSessionID)
			if err != nil {
				t.Fatalf("LoadMetadata: %v", err)
			}
			if meta.Backend != tt.wantBackend {
				t.Errorf("created session backend = %q, want %q", meta.Backend, tt.wantBackend)
			}
		})
	}
}
