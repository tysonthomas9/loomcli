package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// mockGitOps implements GitOps for testing.
type mockGitOps struct {
	resolveFunc            func(name string) (*AgentWorktree, error)
	pushFunc               func(worktreePath, sourceBranch, targetBranch, remote string) (*GitPushResult, error)
	pullFunc               func(worktreePath, currentBranch, sourceBranch, remote string) (*GitPullResult, error)
	createPRFunc           func(worktreePath, sourceBranch, targetBranch, remote string) (*GitPRResult, error)
	resetFunc              func(worktreePath, worktreeName, targetBranch string, force, push bool) (*GitResetResult, error)
	statusFunc             func(worktreePath, targetBranch string) (*GitStatusResult, error)
	getCurrentBranchFunc   func(worktreePath string) (string, error)
	checkGhInstalledFunc   func() error
	setRepoDefaultFunc     func(repoName, branch string) error
	listAgentWorktreesFunc func() ([]AgentWorktree, error)
	diffStatFunc           func(worktreePath, fromRef string) DiffStatResult
	resolveMergeBaseFunc   func(worktreePath, branch string) (string, error)
	diffCommitsFunc        func(worktreePath, mergeBase string, limit int) ([]DiffCommitResult, error)
	diffFilesFunc          func(worktreePath, from, to string) ([]DiffFileResult, error)
	diffFilePatchFunc      func(worktreePath, from, to, path string) (*DiffFilePatchResult, error)
}

func (m *mockGitOps) ResolveAgentWorktree(workspaceID, name string) (*AgentWorktree, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(name)
	}
	return nil, errors.New("not found")
}

func (m *mockGitOps) Push(worktreePath, sourceBranch, targetBranch, remote string) (*GitPushResult, error) {
	if m.pushFunc != nil {
		return m.pushFunc(worktreePath, sourceBranch, targetBranch, remote)
	}
	return &GitPushResult{Success: true, Message: "pushed"}, nil
}

func (m *mockGitOps) Pull(worktreePath, currentBranch, sourceBranch, remote string) (*GitPullResult, error) {
	if m.pullFunc != nil {
		return m.pullFunc(worktreePath, currentBranch, sourceBranch, remote)
	}
	return &GitPullResult{Success: true, Message: "pulled"}, nil
}

func (m *mockGitOps) CreatePR(worktreePath, sourceBranch, targetBranch, remote string) (*GitPRResult, error) {
	if m.createPRFunc != nil {
		return m.createPRFunc(worktreePath, sourceBranch, targetBranch, remote)
	}
	return &GitPRResult{URL: "https://github.com/test/pr/1", Created: true}, nil
}

func (m *mockGitOps) Reset(worktreePath, worktreeName, targetBranch string, force, push bool) (*GitResetResult, error) {
	if m.resetFunc != nil {
		return m.resetFunc(worktreePath, worktreeName, targetBranch, force, push)
	}
	return &GitResetResult{Success: true, Message: "reset done"}, nil
}

func (m *mockGitOps) Status(worktreePath, targetBranch string) (*GitStatusResult, error) {
	if m.statusFunc != nil {
		return m.statusFunc(worktreePath, targetBranch)
	}
	return &GitStatusResult{Branch: "feature", TargetBranch: "main", IsClean: true}, nil
}

func (m *mockGitOps) GetCurrentBranch(worktreePath string) (string, error) {
	if m.getCurrentBranchFunc != nil {
		return m.getCurrentBranchFunc(worktreePath)
	}
	return "feature-branch", nil
}

func (m *mockGitOps) CheckGhInstalled() error {
	if m.checkGhInstalledFunc != nil {
		return m.checkGhInstalledFunc()
	}
	return nil
}

func (m *mockGitOps) SetRepoDefaultBranch(workspaceID, repoName, branch string) error {
	if m.setRepoDefaultFunc != nil {
		return m.setRepoDefaultFunc(repoName, branch)
	}
	return nil
}

func (m *mockGitOps) ListAgentWorktrees(workspaceID string) ([]AgentWorktree, error) {
	if m.listAgentWorktreesFunc != nil {
		return m.listAgentWorktreesFunc()
	}
	return nil, nil
}

func (m *mockGitOps) DiffStat(worktreePath, fromRef string) DiffStatResult {
	if m.diffStatFunc != nil {
		return m.diffStatFunc(worktreePath, fromRef)
	}
	return DiffStatResult{}
}

func (m *mockGitOps) ResolveMergeBase(worktreePath, branch string) (string, error) {
	if m.resolveMergeBaseFunc != nil {
		return m.resolveMergeBaseFunc(worktreePath, branch)
	}
	return "abc123", nil
}

func (m *mockGitOps) DiffCommits(worktreePath, mergeBase string, limit int) ([]DiffCommitResult, error) {
	if m.diffCommitsFunc != nil {
		return m.diffCommitsFunc(worktreePath, mergeBase, limit)
	}
	return []DiffCommitResult{}, nil
}

func (m *mockGitOps) DiffFiles(worktreePath, from, to string) ([]DiffFileResult, error) {
	if m.diffFilesFunc != nil {
		return m.diffFilesFunc(worktreePath, from, to)
	}
	return []DiffFileResult{}, nil
}

func (m *mockGitOps) DiffFilePatch(worktreePath, from, to, path string) (*DiffFilePatchResult, error) {
	if m.diffFilePatchFunc != nil {
		return m.diffFilePatchFunc(worktreePath, from, to, path)
	}
	return &DiffFilePatchResult{}, nil
}

// testWorktree returns a standard AgentWorktree used across tests.
func testWorktree() *AgentWorktree {
	return &AgentWorktree{
		Name:          "test-agent",
		Path:          "/tmp/worktrees/test-agent",
		Branch:        "loomcli-test-agent",
		DefaultBranch: "main",
		Remote:        "origin",
		RepoName:      "myrepo",
		IsWorkspace:   true,
	}
}

// resolveOK returns a mockGitOps that successfully resolves to testWorktree().
func resolveOK() *mockGitOps {
	wt := testWorktree()
	return &mockGitOps{
		resolveFunc: func(name string) (*AgentWorktree, error) {
			return wt, nil
		},
	}
}

// resolveAgentTestHandler builds a minimal handler that exercises resolveAgent
// without depending on any specific git-operation handler.  This lets the
// resolveAgent unit tests remain independent of which handler calls it.
func resolveAgentTestHandler(ops AgentResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := resolveAgent(w, r, ops)
		if ok {
			respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}
	}
}

// --- resolveAgent tests ---

func TestGitResolveAgent_EmptyName(t *testing.T) {
	ops := resolveOK()
	handler := resolveAgentTestHandler(ops)

	req := httptest.NewRequest(http.MethodPost, "/api/agents//git/push", nil)
	req.SetPathValue("name", "")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp["error"] != "missing agent name" {
		t.Errorf("error = %q, want %q", resp["error"], "missing agent name")
	}
}

func TestGitResolveAgent_InvalidName(t *testing.T) {
	ops := resolveOK()
	handler := resolveAgentTestHandler(ops)

	tests := []struct {
		name      string
		agentName string
	}{
		{"contains space", "agent one"},
		{"contains slash", "agent/one"},
		{"contains dot", "agent.one"},
		{"path traversal", "../etc/passwd"},
		{"special chars", "agent@foo!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/agents/invalid/git/push", nil)
			req.SetPathValue("name", tt.agentName)
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			var resp map[string]string
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if resp["error"] != "invalid agent name: must match [a-zA-Z0-9_-]+" {
				t.Errorf("error = %q, want validation error", resp["error"])
			}
		})
	}
}

func TestGitResolveAgent_NotFound(t *testing.T) {
	ops := &mockGitOps{
		resolveFunc: func(name string) (*AgentWorktree, error) {
			return nil, errors.New("not found")
		},
	}
	handler := resolveAgentTestHandler(ops)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/nonexistent/git/push", nil)
	req.SetPathValue("name", "nonexistent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !strings.Contains(resp["error"], "not found") {
		t.Errorf("error = %q, want to contain 'not found'", resp["error"])
	}
}

// --- Push tests ---

func TestGitPush_Success(t *testing.T) {
	svc := &mockAgentService{
		gitPushFunc: func(ctx context.Context, wsID, agentName, target string) (*GitPushResult, error) {
			if target != "" {
				t.Errorf("target = %q, want %q", target, "")
			}
			return &GitPushResult{Success: true, Message: "merged"}, nil
		},
	}
	handler := handleGitPush(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/push", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp GitPushResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Error("expected success to be true")
	}
}

func TestGitPush_CustomTarget(t *testing.T) {
	svc := &mockAgentService{
		gitPushFunc: func(ctx context.Context, wsID, agentName, target string) (*GitPushResult, error) {
			if target != "develop" {
				t.Errorf("target = %q, want %q", target, "develop")
			}
			return &GitPushResult{Success: true, Message: "merged to develop"}, nil
		},
	}
	handler := handleGitPush(svc)

	body := `{"target": "develop"}`
	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/push", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestGitPush_Conflict(t *testing.T) {
	svc := &mockAgentService{
		gitPushFunc: func(ctx context.Context, wsID, agentName, target string) (*GitPushResult, error) {
			return &GitPushResult{
				Success:         false,
				Message:         "merge conflict",
				ConflictedFiles: []string{"file1.go", "file2.go"},
			}, nil
		},
	}
	handler := handleGitPush(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/push", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}

	var resp GitPushResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Success {
		t.Error("expected success to be false")
	}
	if len(resp.ConflictedFiles) != 2 {
		t.Errorf("conflicted files count = %d, want 2", len(resp.ConflictedFiles))
	}
}

func TestGitPush_OperationError(t *testing.T) {
	svc := &mockAgentService{
		gitPushFunc: func(ctx context.Context, wsID, agentName, target string) (*GitPushResult, error) {
			return nil, errors.New("remote unreachable")
		},
	}
	handler := handleGitPush(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/push", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}

// --- Pull tests ---

func TestGitPull_Success(t *testing.T) {
	svc := &mockAgentService{
		gitPullFunc: func(ctx context.Context, wsID, agentName, source string) (*GitPullResult, error) {
			if source != "" {
				t.Errorf("source = %q, want %q", source, "")
			}
			return &GitPullResult{Success: true, Message: "pulled"}, nil
		},
	}
	handler := handleGitPull(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/pull", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp GitPullResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Error("expected success to be true")
	}
}

func TestGitPull_CustomSource(t *testing.T) {
	svc := &mockAgentService{
		gitPullFunc: func(ctx context.Context, wsID, agentName, source string) (*GitPullResult, error) {
			if source != "develop" {
				t.Errorf("source = %q, want %q", source, "develop")
			}
			return &GitPullResult{Success: true, Message: "pulled from develop"}, nil
		},
	}
	handler := handleGitPull(svc)

	body := `{"source": "develop"}`
	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/pull", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestGitPull_Conflict(t *testing.T) {
	svc := &mockAgentService{
		gitPullFunc: func(ctx context.Context, wsID, agentName, source string) (*GitPullResult, error) {
			return &GitPullResult{
				Success:         false,
				Message:         "merge conflict",
				ConflictedFiles: []string{"main.go"},
			}, nil
		},
	}
	handler := handleGitPull(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/pull", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestGitPull_GetCurrentBranchError(t *testing.T) {
	svc := &mockAgentService{
		gitPullFunc: func(ctx context.Context, wsID, agentName, source string) (*GitPullResult, error) {
			return nil, &service.ServiceError{Kind: service.KindInternal, Message: "detached HEAD"}
		},
	}
	handler := handleGitPull(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/pull", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestGitPull_OperationError(t *testing.T) {
	svc := &mockAgentService{
		gitPullFunc: func(ctx context.Context, wsID, agentName, source string) (*GitPullResult, error) {
			return nil, errors.New("network error")
		},
	}
	handler := handleGitPull(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/pull", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}

// --- Sync tests ---

func TestGitSync_Success(t *testing.T) {
	svc := &mockAgentService{
		gitSyncFunc: func(ctx context.Context, wsID, agentName string) (*GitSyncResult, error) {
			return &GitSyncResult{
				PushResult: &GitPushResult{Success: true, Message: "pushed"},
				PullResult: &GitPullResult{Success: true, Message: "pulled"},
			}, nil
		},
	}
	handler := handleGitSync(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/sync", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp GitSyncResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.PushResult == nil {
		t.Fatal("expected PushResult to be non-nil")
	}
	if resp.PullResult == nil {
		t.Fatal("expected PullResult to be non-nil")
	}
	if !resp.PushResult.Success {
		t.Error("expected push success to be true")
	}
	if !resp.PullResult.Success {
		t.Error("expected pull success to be true")
	}
}

func TestGitSync_PushConflict(t *testing.T) {
	svc := &mockAgentService{
		gitSyncFunc: func(ctx context.Context, wsID, agentName string) (*GitSyncResult, error) {
			return &GitSyncResult{
				PushResult: &GitPushResult{
					Success:         false,
					Message:         "conflict",
					ConflictedFiles: []string{"a.go"},
				},
			}, nil
		},
	}
	handler := handleGitSync(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/sync", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}

	var resp GitSyncResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.PushResult == nil {
		t.Fatal("expected PushResult to be non-nil")
	}
	if resp.PullResult != nil {
		t.Error("expected PullResult to be nil when push has conflict")
	}
}

func TestGitSync_PushError(t *testing.T) {
	svc := &mockAgentService{
		gitSyncFunc: func(ctx context.Context, wsID, agentName string) (*GitSyncResult, error) {
			return nil, errors.New("push failed")
		},
	}
	handler := handleGitSync(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/sync", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}

func TestGitSync_PullConflict(t *testing.T) {
	svc := &mockAgentService{
		gitSyncFunc: func(ctx context.Context, wsID, agentName string) (*GitSyncResult, error) {
			return &GitSyncResult{
				PushResult: &GitPushResult{Success: true, Message: "pushed"},
				PullResult: &GitPullResult{
					Success:         false,
					Message:         "conflict on pull",
					ConflictedFiles: []string{"b.go"},
				},
			}, nil
		},
	}
	handler := handleGitSync(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/sync", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}

	var resp GitSyncResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.PushResult == nil {
		t.Fatal("expected PushResult to be non-nil")
	}
	if resp.PullResult == nil {
		t.Fatal("expected PullResult to be non-nil")
	}
}

func TestGitSync_PullError(t *testing.T) {
	svc := &mockAgentService{
		gitSyncFunc: func(ctx context.Context, wsID, agentName string) (*GitSyncResult, error) {
			return nil, errors.New("pull failed")
		},
	}
	handler := handleGitSync(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/sync", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}

func TestGitSync_GetCurrentBranchError(t *testing.T) {
	svc := &mockAgentService{
		gitSyncFunc: func(ctx context.Context, wsID, agentName string) (*GitSyncResult, error) {
			return nil, &service.ServiceError{Kind: service.KindInternal, Message: "detached HEAD"}
		},
	}
	handler := handleGitSync(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/sync", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// --- PR tests ---

func TestGitPR_Created(t *testing.T) {
	svc := &mockAgentService{
		createPRFunc: func(ctx context.Context, wsID, agentName, target string) (*GitPRResult, error) {
			return &GitPRResult{URL: "https://github.com/test/pr/1", Created: true}, nil
		},
	}
	handler := handleGitPR(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/pr", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	var resp GitPRResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Created {
		t.Error("expected Created to be true")
	}
	if resp.URL != "https://github.com/test/pr/1" {
		t.Errorf("URL = %q, want %q", resp.URL, "https://github.com/test/pr/1")
	}
}

func TestGitPR_AlreadyExists(t *testing.T) {
	svc := &mockAgentService{
		createPRFunc: func(ctx context.Context, wsID, agentName, target string) (*GitPRResult, error) {
			return &GitPRResult{URL: "https://github.com/test/pr/1", Created: false, AlreadyExists: true}, nil
		},
	}
	handler := handleGitPR(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/pr", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp GitPRResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Created {
		t.Error("expected Created to be false")
	}
	if !resp.AlreadyExists {
		t.Error("expected AlreadyExists to be true")
	}
}

func TestGitPR_GhNotInstalled(t *testing.T) {
	svc := &mockAgentService{
		createPRFunc: func(ctx context.Context, wsID, agentName, target string) (*GitPRResult, error) {
			return nil, &service.ServiceError{Kind: service.KindUnavailable, Message: "gh CLI not installed"}
		},
	}
	handler := handleGitPR(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/pr", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !strings.Contains(resp["error"], "gh CLI not installed") {
		t.Errorf("error = %q, want to contain 'gh CLI not installed'", resp["error"])
	}
}

func TestGitPR_CustomTarget(t *testing.T) {
	svc := &mockAgentService{
		createPRFunc: func(ctx context.Context, wsID, agentName, target string) (*GitPRResult, error) {
			if target != "develop" {
				t.Errorf("target = %q, want %q", target, "develop")
			}
			return &GitPRResult{URL: "https://github.com/test/pr/2", Created: true}, nil
		},
	}
	handler := handleGitPR(svc)

	body := `{"target": "develop"}`
	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/pr", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
}

func TestGitPR_OperationError(t *testing.T) {
	svc := &mockAgentService{
		createPRFunc: func(ctx context.Context, wsID, agentName, target string) (*GitPRResult, error) {
			return nil, errors.New("API rate limit exceeded")
		},
	}
	handler := handleGitPR(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/pr", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}

// --- Reset tests ---

func TestGitReset_Success(t *testing.T) {
	svc := &mockAgentService{
		gitResetFunc: func(ctx context.Context, wsID, agentName, branch string, force, push bool) (*GitResetResult, error) {
			return &GitResetResult{Success: true, Message: "reset to main", PreviousBranch: "loomcli-test-agent"}, nil
		},
	}
	handler := handleGitReset(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/reset", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp GitResetResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Error("expected success to be true")
	}
}

func TestGitReset_CustomBranch(t *testing.T) {
	svc := &mockAgentService{
		gitResetFunc: func(ctx context.Context, wsID, agentName, branch string, force, push bool) (*GitResetResult, error) {
			if branch != "develop" {
				t.Errorf("branch = %q, want %q", branch, "develop")
			}
			return &GitResetResult{Success: true, Message: "reset to develop"}, nil
		},
	}
	handler := handleGitReset(svc)

	body := `{"branch": "develop"}`
	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/reset", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestGitReset_ForceFlag(t *testing.T) {
	svc := &mockAgentService{
		gitResetFunc: func(ctx context.Context, wsID, agentName, branch string, force, push bool) (*GitResetResult, error) {
			if !force {
				t.Error("expected force to be true")
			}
			return &GitResetResult{Success: true, Message: "force reset"}, nil
		},
	}
	handler := handleGitReset(svc)

	body := `{"force": true}`
	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/reset", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestGitReset_Locked(t *testing.T) {
	svc := &mockAgentService{
		gitResetFunc: func(ctx context.Context, wsID, agentName, branch string, force, push bool) (*GitResetResult, error) {
			return nil, &GitResetLockedError{
				AgentName: "test-agent",
				PID:       12345,
				Duration:  "5m32s",
				TaskID:    "task-abc",
			}
		},
	}
	handler := handleGitReset(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/reset", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusLocked {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusLocked)
	}

	var resp lockedResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Error != "agent locked" {
		t.Errorf("error = %q, want %q", resp.Error, "agent locked")
	}
	if resp.LockInfo.Agent != "test-agent" {
		t.Errorf("LockInfo.Agent = %q, want %q", resp.LockInfo.Agent, "test-agent")
	}
	if resp.LockInfo.PID != 12345 {
		t.Errorf("LockInfo.PID = %d, want %d", resp.LockInfo.PID, 12345)
	}
	if resp.LockInfo.Duration != "5m32s" {
		t.Errorf("LockInfo.Duration = %q, want %q", resp.LockInfo.Duration, "5m32s")
	}
	if resp.LockInfo.TaskID != "task-abc" {
		t.Errorf("LockInfo.TaskID = %q, want %q", resp.LockInfo.TaskID, "task-abc")
	}
}

func TestGitReset_OperationError(t *testing.T) {
	svc := &mockAgentService{
		gitResetFunc: func(ctx context.Context, wsID, agentName, branch string, force, push bool) (*GitResetResult, error) {
			return nil, errors.New("reset failed: dirty worktree")
		},
	}
	handler := handleGitReset(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/reset", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}

// --- Status tests ---

func TestGitStatus_Success(t *testing.T) {
	svc := &mockAgentService{
		gitStatusFunc: func(ctx context.Context, wsID, agentName string) (*GitStatusResult, error) {
			return &GitStatusResult{
				Branch:       "loomcli-test-agent",
				TargetBranch: "main",
				IsClean:      false,
				Ahead:        3,
				Behind:       1,
				ChangedFiles: []string{"file1.go", "file2.go"},
				StashCount:   2,
			}, nil
		},
	}
	handler := handleGitStatus(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/git/status", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp GitStatusResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Branch != "loomcli-test-agent" {
		t.Errorf("Branch = %q, want %q", resp.Branch, "loomcli-test-agent")
	}
	if resp.IsClean {
		t.Error("expected IsClean to be false")
	}
	if resp.Ahead != 3 {
		t.Errorf("Ahead = %d, want %d", resp.Ahead, 3)
	}
	if resp.Behind != 1 {
		t.Errorf("Behind = %d, want %d", resp.Behind, 1)
	}
	if len(resp.ChangedFiles) != 2 {
		t.Errorf("ChangedFiles count = %d, want 2", len(resp.ChangedFiles))
	}
	if resp.StashCount != 2 {
		t.Errorf("StashCount = %d, want %d", resp.StashCount, 2)
	}
}

func TestGitStatus_Error(t *testing.T) {
	svc := &mockAgentService{
		gitStatusFunc: func(ctx context.Context, wsID, agentName string) (*GitStatusResult, error) {
			return nil, errors.New("git not initialized")
		},
	}
	handler := handleGitStatus(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/git/status", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// --- Target Update tests ---

func TestGitTargetUpdate_Success(t *testing.T) {
	svc := &mockAgentService{
		setTargetBranchFunc: func(ctx context.Context, wsID, agentName, branch string) error {
			if branch != "develop" {
				t.Errorf("branch = %q, want %q", branch, "develop")
			}
			return nil
		},
	}
	handler := handleGitTargetUpdate(svc)

	body := `{"branch": "develop"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/test-agent/git/target", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp gitTargetResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Error("expected success to be true")
	}
	if resp.Branch != "develop" {
		t.Errorf("Branch = %q, want %q", resp.Branch, "develop")
	}
}

func TestGitTargetUpdate_MissingBranch(t *testing.T) {
	svc := &mockAgentService{}
	handler := handleGitTargetUpdate(svc)

	body := `{}`
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/test-agent/git/target", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp["error"] != "branch is required" {
		t.Errorf("error = %q, want %q", resp["error"], "branch is required")
	}
}

func TestGitTargetUpdate_NotWorkspaceMode(t *testing.T) {
	svc := &mockAgentService{
		setTargetBranchFunc: func(ctx context.Context, wsID, agentName, branch string) error {
			return &service.ServiceError{Kind: service.KindValidation, Message: "target branch can only be changed in workspace mode"}
		},
	}
	handler := handleGitTargetUpdate(svc)

	body := `{"branch": "develop"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/test-agent/git/target", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !strings.Contains(resp["error"], "workspace mode") {
		t.Errorf("error = %q, want to contain 'workspace mode'", resp["error"])
	}
}

func TestGitTargetUpdate_SetError(t *testing.T) {
	svc := &mockAgentService{
		setTargetBranchFunc: func(ctx context.Context, wsID, agentName, branch string) error {
			return errors.New("branch not found")
		},
	}
	handler := handleGitTargetUpdate(svc)

	body := `{"branch": "nonexistent"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/test-agent/git/target", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestGitTargetUpdate_InvalidBody(t *testing.T) {
	svc := &mockAgentService{}
	handler := handleGitTargetUpdate(svc)

	body := `{invalid json`
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/test-agent/git/target", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp["error"] != "invalid request body" {
		t.Errorf("error = %q, want %q", resp["error"], "invalid request body")
	}
}

// --- Validate Content-Type ---

func TestGitHandlers_ContentType(t *testing.T) {
	svc := &mockAgentService{}

	tests := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
	}{
		{"push", handleGitPush(svc), http.MethodPost, "/api/agents/test-agent/git/push"},
		{"pull", handleGitPull(svc), http.MethodPost, "/api/agents/test-agent/git/pull"},
		{"sync", handleGitSync(svc), http.MethodPost, "/api/agents/test-agent/git/sync"},
		{"pr", handleGitPR(svc), http.MethodPost, "/api/agents/test-agent/git/pr"},
		{"reset", handleGitReset(svc), http.MethodPost, "/api/agents/test-agent/git/reset"},
		{"status", handleGitStatus(svc), http.MethodGet, "/api/agents/test-agent/git/status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.SetPathValue("name", "test-agent")
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
			w := httptest.NewRecorder()

			tt.handler.ServeHTTP(w, req)

			ct := w.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/json")
			}
		})
	}
}

// --- Push All tests ---

func TestGitPushAll_Success(t *testing.T) {
	svc := &mockAgentService{
		gitPushAllFunc: func(ctx context.Context, wsID string) (*GitPushAllResult, error) {
			return &GitPushAllResult{
				Pushed: 2,
				Failed: 0,
				Results: []GitPushAllWorktreeResult{
					{Name: "falcon", Success: true, Message: "merged"},
					{Name: "nova", Success: true, Message: "merged"},
				},
			}, nil
		},
	}
	handler := handleGitPushAll(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/git/push-all", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp GitPushAllResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Pushed != 2 {
		t.Errorf("pushed = %d, want 2", resp.Pushed)
	}
	if resp.Failed != 0 {
		t.Errorf("failed = %d, want 0", resp.Failed)
	}
	if len(resp.Results) != 2 {
		t.Errorf("results count = %d, want 2", len(resp.Results))
	}
}

func TestGitPushAll_PartialFailure(t *testing.T) {
	svc := &mockAgentService{
		gitPushAllFunc: func(ctx context.Context, wsID string) (*GitPushAllResult, error) {
			return &GitPushAllResult{
				Pushed: 1,
				Failed: 1,
				Results: []GitPushAllWorktreeResult{
					{Name: "falcon", Success: true, Message: "merged"},
					{Name: "nova", Success: false, Error: "remote unreachable"},
				},
			}, nil
		},
	}
	handler := handleGitPushAll(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/git/push-all", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp GitPushAllResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Pushed != 1 {
		t.Errorf("pushed = %d, want 1", resp.Pushed)
	}
	if resp.Failed != 1 {
		t.Errorf("failed = %d, want 1", resp.Failed)
	}
}

func TestGitPushAll_EmptyWorktreeList(t *testing.T) {
	svc := &mockAgentService{
		gitPushAllFunc: func(ctx context.Context, wsID string) (*GitPushAllResult, error) {
			return &GitPushAllResult{
				Pushed:  0,
				Failed:  0,
				Results: []GitPushAllWorktreeResult{},
			}, nil
		},
	}
	handler := handleGitPushAll(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/git/push-all", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp GitPushAllResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Pushed != 0 {
		t.Errorf("pushed = %d, want 0", resp.Pushed)
	}
	if resp.Failed != 0 {
		t.Errorf("failed = %d, want 0", resp.Failed)
	}
}

func TestGitPushAll_AllUpToDate(t *testing.T) {
	svc := &mockAgentService{
		gitPushAllFunc: func(ctx context.Context, wsID string) (*GitPushAllResult, error) {
			return &GitPushAllResult{
				Pushed: 0,
				Failed: 0,
				Results: []GitPushAllWorktreeResult{
					{Name: "falcon", Success: true, Message: "already up to date"},
				},
			}, nil
		},
	}
	handler := handleGitPushAll(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/git/push-all", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp GitPushAllResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Pushed != 0 {
		t.Errorf("pushed = %d, want 0", resp.Pushed)
	}
	if resp.Failed != 0 {
		t.Errorf("failed = %d, want 0", resp.Failed)
	}
	if len(resp.Results) != 1 {
		t.Errorf("results count = %d, want 1", len(resp.Results))
	}
	if !resp.Results[0].Success {
		t.Error("expected first result to be success")
	}
}

func TestGitPushAll_ListError(t *testing.T) {
	svc := &mockAgentService{
		gitPushAllFunc: func(ctx context.Context, wsID string) (*GitPushAllResult, error) {
			return nil, errors.New("config not found")
		},
	}
	handler := handleGitPushAll(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/git/push-all", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// --- Workspace-scoped tests ---

func TestResolveAgent_WorkspaceScoped(t *testing.T) {
	var capturedWorkspaceID string
	ops := &mockGitOps{
		resolveFunc: func(name string) (*AgentWorktree, error) {
			return testWorktree(), nil
		},
	}
	// Override ResolveAgentWorktree to capture workspace ID.
	// We use a wrapper approach: set up the mock so the method captures wsID.
	captureOps := &wsCaptureMockGitOps{
		mockGitOps: ops,
		captureResolveWS: func(wsID string) {
			capturedWorkspaceID = wsID
		},
	}

	handler := resolveAgentTestHandler(captureOps)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/push", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws1"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if capturedWorkspaceID != "ws1" {
		t.Errorf("capturedWorkspaceID = %q, want %q", capturedWorkspaceID, "ws1")
	}
}

func TestResolveAgent_NoWorkspace_Returns400(t *testing.T) {
	ops := &mockGitOps{
		resolveFunc: func(name string) (*AgentWorktree, error) {
			t.Fatal("resolveFunc should not be called when workspace ID is empty")
			return nil, nil
		},
	}

	handler := resolveAgentTestHandler(ops)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/test-agent/git/push", nil)
	req.SetPathValue("name", "test-agent")
	// No workspace in context
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleGitPushAll_WorkspaceScoped(t *testing.T) {
	var capturedWSID string
	svc := &mockAgentService{
		gitPushAllFunc: func(ctx context.Context, wsID string) (*GitPushAllResult, error) {
			capturedWSID = wsID
			return &GitPushAllResult{
				Pushed: 1,
				Results: []GitPushAllWorktreeResult{
					{Name: "falcon", Success: true, Message: "merged"},
				},
			}, nil
		},
	}

	handler := handleGitPushAll(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/git/push-all", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-prod"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if capturedWSID != "ws-prod" {
		t.Errorf("capturedWSID = %q, want %q", capturedWSID, "ws-prod")
	}
}

func TestHandleGitTargetUpdate_WorkspaceScoped(t *testing.T) {
	var capturedWSID string
	svc := &mockAgentService{
		setTargetBranchFunc: func(ctx context.Context, wsID, agentName, branch string) error {
			capturedWSID = wsID
			return nil
		},
	}

	handler := handleGitTargetUpdate(svc)

	body := `{"branch": "develop"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/test-agent/git/target", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-staging"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	if capturedWSID != "ws-staging" {
		t.Errorf("capturedWSID = %q, want %q", capturedWSID, "ws-staging")
	}
}

// wsCaptureMockGitOps wraps mockGitOps to capture workspace IDs passed to
// workspace-scoped methods. This allows tests to verify that the correct
// workspace ID flows from the HTTP context through to the interface methods.
type wsCaptureMockGitOps struct {
	*mockGitOps
	captureResolveWS func(wsID string)
	captureListWS    func(wsID string)
	captureSetRepoWS func(wsID string)
}

func (m *wsCaptureMockGitOps) ResolveAgentWorktree(workspaceID, name string) (*AgentWorktree, error) {
	if m.captureResolveWS != nil {
		m.captureResolveWS(workspaceID)
	}
	return m.mockGitOps.ResolveAgentWorktree(workspaceID, name)
}

func (m *wsCaptureMockGitOps) ListAgentWorktrees(workspaceID string) ([]AgentWorktree, error) {
	if m.captureListWS != nil {
		m.captureListWS(workspaceID)
	}
	return m.mockGitOps.ListAgentWorktrees(workspaceID)
}

func (m *wsCaptureMockGitOps) SetRepoDefaultBranch(workspaceID, repoName, branch string) error {
	if m.captureSetRepoWS != nil {
		m.captureSetRepoWS(workspaceID)
	}
	return m.mockGitOps.SetRepoDefaultBranch(workspaceID, repoName, branch)
}

func TestGitPushAll_DefaultRemote(t *testing.T) {
	svc := &mockAgentService{
		gitPushAllFunc: func(ctx context.Context, wsID string) (*GitPushAllResult, error) {
			return &GitPushAllResult{
				Pushed: 1,
				Results: []GitPushAllWorktreeResult{
					{Name: "falcon", Success: true, Message: "merged"},
				},
			}, nil
		},
	}
	handler := handleGitPushAll(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/git/push-all", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp GitPushAllResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Pushed != 1 {
		t.Errorf("pushed = %d, want 1", resp.Pushed)
	}
}
