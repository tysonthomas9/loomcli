package backends

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// --- Type aliases ---

type LoomConfig = config.LoomConfig
type WorkspaceConfig = config.WorkspaceConfig

var (
	RegisterBackend               = cli.RegisterBackend
	SetBackend                    = cli.SetBackend
	IsRegistered                  = cli.IsRegistered
	GetBackendByName              = cli.GetBackendByName
	ListBackends                  = cli.ListBackends
	InvokeAgentNonInteractive     = cli.InvokeAgentNonInteractive
	FilteredEnv                   = cli.FilteredEnv
	ResetWorkspaceRuntimeDirCache = cli.ResetWorkspaceRuntimeDirCache
)

// backendMu provides access to the cli backend registry mutex.
var backendMu = cli.TestingBackendMu()

// backends provides access to the cli backend registry map.
var backends = cli.TestingBackends()

// activeBackend provides access to the active backend name.
// Note: use SetBackend / GetBackendByName for most operations.
var activeBackend = cli.TestingActiveBackend()

// resetBackendState saves and restores backend state for test isolation.
func resetBackendState(t *testing.T) {
	t.Helper()
	cli.TestingResetBackendState(t)
}

// --- mockBackend implements cli.Backend for testing ---

type mockBackend struct {
	name                string
	interactiveCalls    []mockCall
	nonInteractiveCalls []mockNonInteractiveCall
	interactiveErr      error
	nonInteractiveErr   error
}

type mockCall struct {
	workDir, prompt, agentName string
}

type mockNonInteractiveCall struct {
	workDir, prompt, agentName string
	shutdown                   <-chan struct{}
	collector                  *usage.Collector
}

func (m *mockBackend) Name() string { return m.name }

func (m *mockBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	m.interactiveCalls = append(m.interactiveCalls, mockCall{workDir, prompt, agentName})
	return m.interactiveErr
}

func (m *mockBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	m.nonInteractiveCalls = append(m.nonInteractiveCalls, mockNonInteractiveCall{workDir, prompt, agentName, shutdown, collector})
	return m.nonInteractiveErr
}

// --- Invoker mock helpers ---

// installClaudeNonInteractiveMock installs a mock for claudeNonInteractiveInvoker.
func installClaudeNonInteractiveMock(t *testing.T, fn func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error) {
	t.Helper()
	orig := claudeNonInteractiveInvoker
	claudeNonInteractiveInvoker = fn
	t.Cleanup(func() { claudeNonInteractiveInvoker = orig })
}

func installCodexInvokerMock(t *testing.T, fn func(workDir, prompt, agentName string) error) {
	t.Helper()
	orig := codexInvoker
	codexInvoker = fn
	t.Cleanup(func() { codexInvoker = orig })
}

func installCodexNonInteractiveMock(t *testing.T, fn func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error) {
	t.Helper()
	orig := codexNonInteractiveInvoker
	codexNonInteractiveInvoker = fn
	t.Cleanup(func() { codexNonInteractiveInvoker = orig })
}

func installOpenCodeInvokerMock(t *testing.T, fn func(workDir, prompt, agentName string) error) {
	t.Helper()
	orig := openCodeInvoker
	openCodeInvoker = fn
	t.Cleanup(func() { openCodeInvoker = orig })
}

func installOpenCodeNonInteractiveMock(t *testing.T, fn func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error) {
	t.Helper()
	orig := openCodeNonInteractiveInvoker
	openCodeNonInteractiveInvoker = fn
	t.Cleanup(func() { openCodeNonInteractiveInvoker = orig })
}

// SetupMockClaudeInvoker installs a mock claude invoker for tests.
type MockClaudeInvokerRecorder struct {
	mu          sync.Mutex
	Invocations []struct {
		WorkDir   string
		Prompt    string
		AgentName string
	}
	ReturnErr error
}

func SetupMockClaudeInvoker(t *testing.T, returnErr error) *MockClaudeInvokerRecorder {
	t.Helper()
	recorder := &MockClaudeInvokerRecorder{ReturnErr: returnErr}
	orig := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		recorder.mu.Lock()
		recorder.Invocations = append(recorder.Invocations, struct {
			WorkDir   string
			Prompt    string
			AgentName string
		}{workDir, prompt, agentName})
		recorder.mu.Unlock()
		return recorder.ReturnErr
	}
	t.Cleanup(func() { claudeInvoker = orig })
	return recorder
}

// --- lowercase alias for exported function ---

func resolveNotifyToken() string { return ResolveNotifyToken() }

func containsSubstring(slice []string, substr string) bool {
	for _, s := range slice {
		if len(s) >= len(substr) {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}

func setupWorkspaceConfig(t *testing.T, cfg *config.LoomConfig) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv("LOOM_FLEET_DB_ACTOR", "test")
	config.InvalidateConfigCache()
	oldResolver := cli.TestingResetDefaultResolver()

	ctx := context.Background()
	handle, err := bootstrap.OpenStore(ctx, configDir, nil)
	if err != nil {
		t.Fatalf("open fleet-db store: %v", err)
	}
	t.Cleanup(func() {
		_ = handle.Close()
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
		if _, err := handle.Store.Workspaces().Create(ctx, store.WorkspaceCreate{
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
			if _, err := handle.Store.Repos().Create(ctx, store.RepoCreate{
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
	config.InvalidateConfigCache()
	cli.ResetWorkspaceRuntimeDirCache()
}

func firstRepoDefaultBranch(repos []config.RepoConfig) string {
	for _, repo := range repos {
		if repo.DefaultBranch != "" {
			return repo.DefaultBranch
		}
	}
	return ""
}
