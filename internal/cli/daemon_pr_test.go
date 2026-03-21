package cli

import (
	"context"
	"fmt"
	"testing"
)

// stubGhAvailable replaces ghAvailable for the duration of the test.
func stubGhAvailable(t *testing.T, available bool) {
	t.Helper()
	orig := ghAvailable
	ghAvailable = func() bool { return available }
	t.Cleanup(func() { ghAvailable = orig })
}

func TestGhAvailable_Installed(t *testing.T) {
	resetGhAvailableCache()
	t.Cleanup(resetGhAvailableCache)

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "gh", Args: []string{"--version"}, Stdout: "gh version 2.40.0\n"},
	})
	cmdMock.Install()

	if !defaultGhAvailable() {
		t.Error("expected ghAvailable to return true when gh is installed")
	}
}

func TestGhAvailable_NotInstalled(t *testing.T) {
	resetGhAvailableCache()
	t.Cleanup(resetGhAvailableCache)

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "gh", Args: []string{"--version"}, Err: fmt.Errorf("not found")},
	})
	cmdMock.Install()

	if defaultGhAvailable() {
		t.Error("expected ghAvailable to return false when gh is not installed")
	}
}

func TestRemoteBranchPushed_Exists(t *testing.T) {
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"ls-remote", "--heads", "origin", "epic/loomcli-mzr"},
			Stdout: "abc123\trefs/heads/epic/loomcli-mzr\n"},
	})
	cmdMock.Install()

	if !remoteBranchPushed("/repo", "epic/loomcli-mzr") {
		t.Error("expected true when branch exists on remote")
	}
}

func TestRemoteBranchPushed_NotExists(t *testing.T) {
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"ls-remote", "--heads", "origin", "epic/loomcli-mzr"},
			Stdout: ""},
	})
	cmdMock.Install()

	if remoteBranchPushed("/repo", "epic/loomcli-mzr") {
		t.Error("expected false when branch not on remote")
	}
}

func TestRemoteBranchPushed_CommandFails(t *testing.T) {
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"ls-remote", "--heads", "origin", "epic/loomcli-mzr"},
			Err: fmt.Errorf("network error")},
	})
	cmdMock.Install()

	if remoteBranchPushed("/repo", "epic/loomcli-mzr") {
		t.Error("expected false when command fails")
	}
}

func TestGetOpenPRForBranch_PRExists(t *testing.T) {
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "gh", Args: []string{"pr", "list", "--head", "epic/loomcli-mzr", "--state", "open", "--json", "url", "--limit", "1"},
			Stdout: `[{"url":"https://github.com/org/repo/pull/42"}]`},
	})
	cmdMock.Install()

	url, err := getOpenPRForBranch("/repo", "epic/loomcli-mzr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://github.com/org/repo/pull/42" {
		t.Errorf("expected PR URL, got %q", url)
	}
}

func TestGetOpenPRForBranch_NoPR(t *testing.T) {
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "gh", Args: []string{"pr", "list", "--head", "epic/loomcli-mzr", "--state", "open", "--json", "url", "--limit", "1"},
			Stdout: "[]"},
	})
	cmdMock.Install()

	url, err := getOpenPRForBranch("/repo", "epic/loomcli-mzr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty URL, got %q", url)
	}
}

func TestGetOpenPRForBranch_CommandFails(t *testing.T) {
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "gh", Args: []string{"pr", "list", "--head", "epic/loomcli-mzr", "--state", "open", "--json", "url", "--limit", "1"},
			Err: fmt.Errorf("gh failed")},
	})
	cmdMock.Install()

	_, err := getOpenPRForBranch("/repo", "epic/loomcli-mzr")
	if err == nil {
		t.Fatal("expected error when command fails")
	}
}

func TestGetEpicInfo_WithChildren(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.GetIssueResult = &BdIssue{ID: "loomcli-mzr", Title: "Daemon-Managed Epic Branches"}
	mock.ListResult = []BdIssue{
		{ID: "loomcli-mzr.1", Title: "Epic assignment logic", Status: "closed"},
		{ID: "loomcli-mzr.2", Title: "Branch creation", Status: "closed"},
		{ID: "loomcli-mzr.5", Title: "Auto-create PR", Status: "in_progress"},
	}
	setDefaultTracker(mock)

	info, err := getEpicInfo("loomcli-mzr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Title != "Daemon-Managed Epic Branches" {
		t.Errorf("title = %q, want 'Daemon-Managed Epic Branches'", info.Title)
	}
	if info.ID != "loomcli-mzr" {
		t.Errorf("id = %q, want 'loomcli-mzr'", info.ID)
	}
	if len(info.Children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(info.Children))
	}
	if info.Children[0].Status != "closed" {
		t.Errorf("child 0 status = %q, want 'closed'", info.Children[0].Status)
	}
	if info.Children[2].Status != "in_progress" {
		t.Errorf("child 2 status = %q, want 'in_progress'", info.Children[2].Status)
	}
}

func TestGetEpicInfo_NoChildren(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.GetIssueResult = &BdIssue{ID: "loomcli-xyz", Title: "Empty Epic"}
	mock.ListResult = []BdIssue{}
	setDefaultTracker(mock)

	info, err := getEpicInfo("loomcli-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Title != "Empty Epic" {
		t.Errorf("title = %q, want 'Empty Epic'", info.Title)
	}
	if len(info.Children) != 0 {
		t.Errorf("expected 0 children, got %d", len(info.Children))
	}
}

func TestGetEpicInfo_GetIssueFails(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.GetIssueErr = fmt.Errorf("bd failed")
	setDefaultTracker(mock)

	_, err := getEpicInfo("loomcli-mzr")
	if err == nil {
		t.Fatal("expected error when GetIssue fails")
	}
}

func TestGetEpicInfo_ListChildrenFails(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.GetIssueResult = &BdIssue{ID: "loomcli-mzr", Title: "Test Epic"}
	mock.ListErr = fmt.Errorf("list failed")
	setDefaultTracker(mock)

	_, err := getEpicInfo("loomcli-mzr")
	if err == nil {
		t.Fatal("expected error when List fails")
	}
}

func TestBuildPRBody_MixedStatuses(t *testing.T) {
	info := &epicPRInfo{
		Title: "Test Epic",
		ID:    "loomcli-abc",
		Children: []epicChild{
			{ID: "loomcli-abc.1", Title: "Task One", Status: "closed"},
			{ID: "loomcli-abc.2", Title: "Task Two", Status: "open"},
			{ID: "loomcli-abc.3", Title: "Task Three", Status: "in_progress"},
		},
	}

	body := buildPRBody(info)

	if !containsSubstring([]string{body}, "## Epic: Test Epic") {
		t.Error("missing epic title")
	}
	if !containsSubstring([]string{body}, "- [x] loomcli-abc.1: Task One (closed)") {
		t.Error("closed task should have [x]")
	}
	if !containsSubstring([]string{body}, "- [ ] loomcli-abc.2: Task Two (open)") {
		t.Error("open task should have [ ]")
	}
	if !containsSubstring([]string{body}, "- [ ] loomcli-abc.3: Task Three (in_progress)") {
		t.Error("in_progress task should have [ ]")
	}
	if !containsSubstring([]string{body}, "*Auto-created by loom daemon*") {
		t.Error("missing footer")
	}
}

func TestBuildPRBody_AllComplete(t *testing.T) {
	info := &epicPRInfo{
		Title: "Done Epic",
		ID:    "loomcli-done",
		Children: []epicChild{
			{ID: "task-1", Title: "First", Status: "closed"},
			{ID: "task-2", Title: "Second", Status: "closed"},
		},
	}

	body := buildPRBody(info)

	if !containsSubstring([]string{body}, "- [x] task-1") {
		t.Error("all tasks should have [x]")
	}
	if !containsSubstring([]string{body}, "- [x] task-2") {
		t.Error("all tasks should have [x]")
	}
	if containsSubstring([]string{body}, "- [ ]") {
		t.Error("should not have any unchecked tasks")
	}
}

func TestBuildPRBody_NoChildren(t *testing.T) {
	info := &epicPRInfo{
		Title: "Empty Epic",
		ID:    "loomcli-empty",
	}

	body := buildPRBody(info)

	if !containsSubstring([]string{body}, "No tasks yet") {
		t.Error("should show 'No tasks yet' for empty epic")
	}
}

func TestCreateEpicPR_Success(t *testing.T) {
	info := &epicPRInfo{
		Title: "My Epic",
		ID:    "loomcli-mzr",
		Children: []epicChild{
			{ID: "task-1", Title: "First Task", Status: "open"},
		},
	}

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "gh", Stdout: "https://github.com/org/repo/pull/99\n"},
	})
	cmdMock.Install()

	url, err := createEpicPR("/repo", "loomcli-mzr", "epic/loomcli-mzr", info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://github.com/org/repo/pull/99" {
		t.Errorf("url = %q, want PR URL", url)
	}

	// Verify args passed to gh
	calls := cmdMock.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	args := calls[0].Args
	if !containsSubstring(args, "--base") || !containsSubstring(args, "main") {
		t.Error("should pass --base main (default)")
	}
	if !containsSubstring(args, "--head") || !containsSubstring(args, "epic/loomcli-mzr") {
		t.Error("should pass --head epic/loomcli-mzr")
	}
	if !containsSubstring(args, "--title") {
		t.Error("should pass --title")
	}
}

func TestCreateEpicPR_CustomDefaultBranch(t *testing.T) {
	t.Setenv("LOOM_DEFAULT_BRANCH", "develop")

	info := &epicPRInfo{
		Title: "My Epic",
		ID:    "loomcli-mzr",
		Children: []epicChild{
			{ID: "task-1", Title: "First Task", Status: "open"},
		},
	}

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "gh", Stdout: "https://github.com/org/repo/pull/100\n"},
	})
	cmdMock.Install()

	url, err := createEpicPR("/repo", "loomcli-mzr", "epic/loomcli-mzr", info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://github.com/org/repo/pull/100" {
		t.Errorf("url = %q, want PR URL", url)
	}

	calls := cmdMock.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	args := calls[0].Args
	if !containsSubstring(args, "--base") || !containsSubstring(args, "develop") {
		t.Errorf("should pass --base develop when LOOM_DEFAULT_BRANCH=develop, got args: %v", args)
	}
}

func TestCreateEpicPR_CommandFails(t *testing.T) {
	info := &epicPRInfo{Title: "My Epic", ID: "loomcli-mzr"}

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "gh", Err: fmt.Errorf("rate limited")},
	})
	cmdMock.Install()

	_, err := createEpicPR("/repo", "loomcli-mzr", "epic/loomcli-mzr", info)
	if err == nil {
		t.Fatal("expected error when command fails")
	}
}

func TestStoreExternalRef_Success(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	setDefaultTracker(mock)

	err := storeExternalRef("loomcli-mzr", "https://github.com/org/repo/pull/42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.Called("UpdateExternalRef") {
		t.Error("expected UpdateExternalRef to be called")
	}
}

func TestStoreExternalRef_CommandFails(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.UpdateExternalRefErr = fmt.Errorf("bd failed")
	setDefaultTracker(mock)

	err := storeExternalRef("loomcli-mzr", "https://example.com")
	if err == nil {
		t.Fatal("expected error when command fails")
	}
}

func TestEnsureEpicPR_FullFlow(t *testing.T) {
	stubGhAvailable(t, true)
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.GetIssueResult = &BdIssue{ID: "loomcli-mzr", Title: "Test Epic"}
	mock.ListResult = []BdIssue{
		{ID: "task-1", Title: "First", Status: "closed"},
		{ID: "task-2", Title: "Second", Status: "open"},
	}
	setDefaultTracker(mock)

	cmdMock := NewCommandMock(t, []CommandStub{
		// remoteBranchPushed
		{Name: "git", Args: []string{"ls-remote", "--heads", "origin", "epic/loomcli-mzr"},
			Stdout: "abc123\trefs/heads/epic/loomcli-mzr\n"},
		// getOpenPRForBranch - no existing PR
		{Name: "gh", Args: []string{"pr", "list", "--head", "epic/loomcli-mzr", "--state", "open", "--json", "url", "--limit", "1"},
			Stdout: "[]"},
		// createEpicPR
		{Name: "gh", Stdout: "https://github.com/org/repo/pull/99\n"},
	})
	cmdMock.Install()

	err := EnsureEpicPR("/repo", "loomcli-mzr", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.Called("GetIssue") {
		t.Error("expected GetIssue to be called")
	}
	if !mock.Called("UpdateExternalRef") {
		t.Error("expected UpdateExternalRef to be called")
	}
}

func TestEnsureEpicPR_GhNotAvailable(t *testing.T) {
	stubGhAvailable(t, false)

	// No commands should be called at all
	cmdMock := NewCommandMock(t, []CommandStub{})
	cmdMock.Install()

	err := EnsureEpicPR("/repo", "loomcli-mzr", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureEpicPR_BranchNotPushed(t *testing.T) {
	stubGhAvailable(t, true)

	cmdMock := NewCommandMock(t, []CommandStub{
		// remoteBranchPushed - not pushed yet
		{Name: "git", Args: []string{"ls-remote", "--heads", "origin", "epic/loomcli-mzr"},
			Stdout: ""},
	})
	cmdMock.Install()

	err := EnsureEpicPR("/repo", "loomcli-mzr", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureEpicPR_PRAlreadyExists(t *testing.T) {
	stubGhAvailable(t, true)
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	setDefaultTracker(mock)

	cmdMock := NewCommandMock(t, []CommandStub{
		// remoteBranchPushed
		{Name: "git", Args: []string{"ls-remote", "--heads", "origin", "epic/loomcli-mzr"},
			Stdout: "abc123\trefs/heads/epic/loomcli-mzr\n"},
		// getOpenPRForBranch - PR exists
		{Name: "gh", Args: []string{"pr", "list", "--head", "epic/loomcli-mzr", "--state", "open", "--json", "url", "--limit", "1"},
			Stdout: `[{"url":"https://github.com/org/repo/pull/42"}]`},
	})
	cmdMock.Install()

	err := EnsureEpicPR("/repo", "loomcli-mzr", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.Called("UpdateExternalRef") {
		t.Error("expected UpdateExternalRef to be called to store existing PR URL")
	}
}

func TestEnsureEpicPR_MergedPR_CreatesNew(t *testing.T) {
	stubGhAvailable(t, true)
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.GetIssueResult = &BdIssue{ID: "loomcli-mzr", Title: "Test Epic"}
	mock.ListResult = []BdIssue{}
	setDefaultTracker(mock)

	cmdMock := NewCommandMock(t, []CommandStub{
		// remoteBranchPushed
		{Name: "git", Args: []string{"ls-remote", "--heads", "origin", "epic/loomcli-mzr"},
			Stdout: "abc123\trefs/heads/epic/loomcli-mzr\n"},
		// getOpenPRForBranch - no open PR (old one was merged)
		{Name: "gh", Args: []string{"pr", "list", "--head", "epic/loomcli-mzr", "--state", "open", "--json", "url", "--limit", "1"},
			Stdout: "[]"},
		// createEpicPR - creates new PR
		{Name: "gh", Stdout: "https://github.com/org/repo/pull/100\n"},
	})
	cmdMock.Install()

	err := EnsureEpicPR("/repo", "loomcli-mzr", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureEpicPR_PRCreateFails_ReturnsError(t *testing.T) {
	stubGhAvailable(t, true)
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.GetIssueResult = &BdIssue{ID: "loomcli-mzr", Title: "Test Epic"}
	mock.ListResult = []BdIssue{}
	setDefaultTracker(mock)

	cmdMock := NewCommandMock(t, []CommandStub{
		// remoteBranchPushed
		{Name: "git", Stdout: "abc123\trefs/heads/epic/loomcli-mzr\n"},
		// getOpenPRForBranch - no PR
		{Name: "gh", Stdout: "[]"},
		// createEpicPR - fails (rate limited)
		{Name: "gh", Err: fmt.Errorf("rate limited")},
	})
	cmdMock.Install()

	err := EnsureEpicPR("/repo", "loomcli-mzr", nil)
	if err == nil {
		t.Fatal("expected error when PR creation fails")
	}
	if !containsSubstring([]string{err.Error()}, "failed to create PR") {
		t.Errorf("error should mention PR creation, got: %v", err)
	}
}

func TestGetEpicInfo_VerifiesListOpts(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	var capturedOpts ListOpts
	mock := NewMockTracker()
	mock.GetIssueResult = &BdIssue{ID: "epic-123", Title: "Test"}
	mock.ListFunc = func(_ context.Context, opts ListOpts) ([]BdIssue, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultTracker(mock)

	_, err := getEpicInfo("epic-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedOpts.ParentID != "epic-123" {
		t.Errorf("ListOpts.ParentID = %q, want 'epic-123'", capturedOpts.ParentID)
	}
	if capturedOpts.Limit != 0 {
		t.Errorf("ListOpts.Limit = %d, want 0 (no limit)", capturedOpts.Limit)
	}
}
