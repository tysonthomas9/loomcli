package doctor

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type CommandResult = cli.CommandResult
type LockInfo = cli.LockInfo
type LoomConfig = config.LoomConfig
type WorkspaceConfig = config.WorkspaceConfig
type FleetDBServerConfig = config.FleetDBServerConfig
type MockExecRunner = clitest.MockExecRunner
type MockIssueBackend = clitest.MockIssueBackend

const LockFileName = cli.LockFileName

var ResetWorkspaceRuntimeDirCache = cli.ResetWorkspaceRuntimeDirCache

func NewTestDeps(t *testing.T) (*cli.Deps, *clitest.MockGitRunner, *clitest.MockExecRunner, *clitest.MockFileSystem, *clitest.MockIssueBackend) {
	return clitest.NewTestDeps(t)
}

func installExecMock(t *testing.T, m *clitest.MockExecRunner) {
	t.Helper()
	dd := cli.TestingGetDefaultDeps()
	orig := dd.Exec
	dd.Exec = m
	t.Cleanup(func() { dd.Exec = orig })
}

func setupWorkspaceConfig(t *testing.T, cfg *config.LoomConfig) {
	t.Helper()
	setupWorkspaceConfigInDir(t, t.TempDir(), cfg)
}

func setupWorkspaceConfigInDir(t *testing.T, configDir string, cfg *config.LoomConfig) {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "")
	t.Setenv("LOOM_FLEET_DB_ACTOR", "test")
	config.InvalidateConfigCache()
	cli.ResetWorkspaceRuntimeDirCache()
	oldResolver := cli.TestingResetDefaultResolver()

	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() {
		_ = st.Close()
		cli.TestingSetDefaultResolver(oldResolver)
		config.InvalidateConfigCache()
		cli.ResetWorkspaceRuntimeDirCache()
	})

	state := &bootstrap.StateCache{Workspaces: make(map[string]bootstrap.WorkspaceLocalState)}
	if cfg.DefaultWorkspace != "" {
		state.LastWorkspace = strings.ToUpper(cfg.DefaultWorkspace)
	}

	names := make([]string, 0, len(cfg.Workspaces))
	for name := range cfg.Workspaces {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ws := cfg.Workspaces[name]
		key := strings.ToUpper(name)
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
			Key:           key,
			Name:          name,
			DefaultBranch: firstRepoDefaultBranch(ws.Repos),
		}); err != nil {
			t.Fatalf("create workspace %s: %v", key, err)
		}

		localRepos := make(map[string]string, len(ws.Repos))
		for _, repo := range ws.Repos {
			sourceRepoID := repo.SourceRepoID
			if sourceRepoID == "" {
				sourceRepoID = repo.Name
			}
			if _, err := st.Repos().Create(ctx, store.RepoCreate{
				WorkspaceKey:  key,
				Name:          repo.Name,
				Remote:        repo.Remote,
				DefaultBranch: repo.DefaultBranch,
				Groups:        repo.Groups,
				SourceRepoID:  sourceRepoID,
			}); err != nil {
				t.Fatalf("create repo %s/%s: %v", key, repo.Name, err)
			}
			localRepos[repo.Name] = repo.Path
		}

		state.Workspaces[key] = bootstrap.WorkspaceLocalState{Path: ws.Path, Repos: localRepos}
	}
	if err := bootstrap.SaveStateCache(state); err != nil {
		t.Fatalf("save state cache: %v", err)
	}
	if cfg.DefaultWorkspace != "" {
		t.Setenv("LOOM_WORKSPACE", strings.ToUpper(cfg.DefaultWorkspace))
	}
	if _, err := config.TestingPrimeConfigCacheFromStore(ctx, st); err != nil {
		t.Fatalf("prime config cache: %v", err)
	}
	if cfg.DefaultWorkspace != "" {
		activeKey := strings.ToUpper(cfg.DefaultWorkspace)
		if ws, ok := cfg.Workspaces[cfg.DefaultWorkspace]; ok {
			if _, err := config.TestingPrimeDaemonConfigCacheFromStore(ctx, st, activeKey, ws.Path); err != nil {
				t.Fatalf("prime daemon config cache: %v", err)
			}
		}
	}
	cli.ResetWorkspaceRuntimeDirCache()
	cli.TestingResetDefaultResolver()
}

func firstRepoDefaultBranch(repos []config.RepoConfig) string {
	for _, repo := range repos {
		if repo.DefaultBranch != "" {
			return repo.DefaultBranch
		}
	}
	return ""
}
