package cli

import (
	"os"
	"testing"
)

func TestSyncSingleWorkspace_PushAndPull(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := tmpDir + "/.loom"
	os.MkdirAll(configDir, 0755)

	wsDir := tmpDir + "/ws"
	repo1 := wsDir + "/api"
	os.MkdirAll(repo1+"/.git", 0755)

	configContent := `workspaces:
  ws1:
    path: ` + wsDir + `
    repos:
      - name: api
        path: ` + repo1 + `
        default_branch: main
`
	os.WriteFile(configDir+"/config.yaml", []byte(configContent), 0644)

	SetupTestEnv(t, map[string]string{
		"LOOM_CONFIG_DIR": configDir,
	})

	origPushOnly := syncPushOnly
	origPullOnly := syncPullOnly
	t.Cleanup(func() {
		syncPushOnly = origPushOnly
		syncPullOnly = origPullOnly
	})
	syncPushOnly = false
	syncPullOnly = false

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// Push phase: fetch, stash, checkout, pull, merge, push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/api-branch", "-m", "Merge api-branch into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		// Pull phase: fetch, merge, push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "api-branch"}, Err: nil},
	})
	outputMock.Install()

	cmdMock := NewCommandMock(t, []CommandStub{
		// DiscoverWorktrees: GetCurrentBranch for api
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
		// Push phase: stash list x2 + HasCommitsBetweenRemote
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"log", "main..origin/api-branch", "--oneline"}, Stdout: "abc commit\n"},
	})
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	resolver, err := NewResolver()
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}
	if err := resolver.SetWorkspace("ws1"); err != nil {
		t.Fatalf("failed to set workspace: %v", err)
	}

	syncSingleWorkspace(resolver)
}

func TestSyncSingleWorkspace_PushOnly(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := tmpDir + "/.loom"
	os.MkdirAll(configDir, 0755)

	wsDir := tmpDir + "/ws"
	repo1 := wsDir + "/api"
	os.MkdirAll(repo1+"/.git", 0755)

	configContent := `workspaces:
  ws1:
    path: ` + wsDir + `
    repos:
      - name: api
        path: ` + repo1 + `
        default_branch: main
`
	os.WriteFile(configDir+"/config.yaml", []byte(configContent), 0644)

	SetupTestEnv(t, map[string]string{
		"LOOM_CONFIG_DIR": configDir,
	})

	origPushOnly := syncPushOnly
	origPullOnly := syncPullOnly
	t.Cleanup(func() {
		syncPushOnly = origPushOnly
		syncPullOnly = origPullOnly
	})
	syncPushOnly = true
	syncPullOnly = false

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// Push phase only
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/api-branch", "-m", "Merge api-branch into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
	})
	outputMock.Install()

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"log", "main..origin/api-branch", "--oneline"}, Stdout: "abc commit\n"},
	})
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	resolver, err := NewResolver()
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}
	if err := resolver.SetWorkspace("ws1"); err != nil {
		t.Fatalf("failed to set workspace: %v", err)
	}

	syncSingleWorkspace(resolver)
}

func TestSyncSingleWorkspace_PullOnly(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := tmpDir + "/.loom"
	os.MkdirAll(configDir, 0755)

	wsDir := tmpDir + "/ws"
	repo1 := wsDir + "/api"
	os.MkdirAll(repo1+"/.git", 0755)

	configContent := `workspaces:
  ws1:
    path: ` + wsDir + `
    repos:
      - name: api
        path: ` + repo1 + `
        default_branch: main
`
	os.WriteFile(configDir+"/config.yaml", []byte(configContent), 0644)

	SetupTestEnv(t, map[string]string{
		"LOOM_CONFIG_DIR": configDir,
	})

	origPushOnly := syncPushOnly
	origPullOnly := syncPullOnly
	t.Cleanup(func() {
		syncPushOnly = origPushOnly
		syncPullOnly = origPullOnly
	})
	syncPushOnly = false
	syncPullOnly = true

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// Pull phase only
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "api-branch"}, Err: nil},
	})
	outputMock.Install()

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
	})
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	resolver, err := NewResolver()
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}
	if err := resolver.SetWorkspace("ws1"); err != nil {
		t.Fatalf("failed to set workspace: %v", err)
	}

	syncSingleWorkspace(resolver)
}
