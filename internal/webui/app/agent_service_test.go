package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// requireServiceError is a test helper that asserts err is a *service.ServiceError
// with the expected kind. It returns the ServiceError for further inspection.
func requireServiceError(t *testing.T, err error, wantKind service.ErrorKind) *service.ServiceError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with kind %s, got nil", wantKind)
	}
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *service.ServiceError, got %T: %v", err, err)
	}
	if svcErr.Kind != wantKind {
		t.Fatalf("ServiceError.Kind = %q, want %q (message: %s)", svcErr.Kind, wantKind, svcErr.Message)
	}
	return svcErr
}

// --- Agent name validation ---

func TestAgentService_ValidateAgentName(t *testing.T) {
	svc := svcimpl.NewAgentService(&mockGitOps{}, nil, nil, nil)
	ctx := context.Background()

	t.Run("empty name returns ErrValidation", func(t *testing.T) {
		_, err := svc.GetDiffStat(ctx, "ws", "")
		requireServiceError(t, err, service.KindValidation)
	})

	t.Run("invalid characters return ErrValidation", func(t *testing.T) {
		badNames := []string{
			"agent one",     // space
			"agent/one",     // slash
			"agent.one",     // dot
			"../etc/passwd", // path traversal
			"agent@foo!",    // special chars
		}
		for _, name := range badNames {
			_, err := svc.GetDiffStat(ctx, "ws", name)
			requireServiceError(t, err, service.KindValidation)
		}
	})

	t.Run("valid names succeed", func(t *testing.T) {
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return testWorktree(), nil
			},
			diffStatFunc: func(worktreePath, fromRef string) ops.DiffStatResult {
				return ops.DiffStatResult{}
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		validNames := []string{"alpha", "test-agent", "agent_1", "ABC", "a-b_c-123"}
		for _, name := range validNames {
			_, err := svc.GetDiffStat(ctx, "ws", name)
			if err != nil {
				t.Errorf("valid name %q returned error: %v", name, err)
			}
		}
	})
}

// --- GetTerminalInfo ---

func TestAgentService_GetTerminalInfo(t *testing.T) {
	ctx := context.Background()

	t.Run("nil termMgr returns ErrUnavailable", func(t *testing.T) {
		svc := svcimpl.NewAgentService(&mockGitOps{}, nil, nil, nil)
		_, err := svc.GetTerminalInfo(ctx, "ws", "agent1")
		requireServiceError(t, err, service.KindUnavailable)
	})

	t.Run("valid agent returns archive mode when no session found", func(t *testing.T) {
		mgr, err := terminal.NewAgentTmuxManager(0)
		if err == terminal.ErrTmuxNotFound {
			t.Skip("tmux not installed, skipping test")
		}
		if err != nil {
			t.Fatalf("NewAgentTmuxManager: %v", err)
		}
		defer mgr.Shutdown()

		svc := svcimpl.NewAgentService(&mockGitOps{}, mgr, nil, nil)
		result, err := svc.GetTerminalInfo(ctx, "ws", "nonexistent-agent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Mode != service.AgentTerminalModeArchive {
			t.Errorf("Mode = %q, want %q", result.Mode, service.AgentTerminalModeArchive)
		}
		if result.Agent != "nonexistent-agent" {
			t.Errorf("Agent = %q, want %q", result.Agent, "nonexistent-agent")
		}
	})
}

// --- GenerateTerminalToken ---

func TestAgentService_GenerateTerminalToken(t *testing.T) {
	ctx := context.Background()

	t.Run("nil termAuth returns ErrUnavailable", func(t *testing.T) {
		svc := svcimpl.NewAgentService(&mockGitOps{}, nil, nil, nil)
		_, err := svc.GenerateTerminalToken(ctx, "test-ws", "agent1", "user1")
		requireServiceError(t, err, service.KindUnavailable)
	})

	t.Run("valid generates token", func(t *testing.T) {
		ta, err := realtime.NewTerminalAuth()
		if err != nil {
			t.Fatalf("NewTerminalAuth: %v", err)
		}
		defer ta.Stop()

		svc := svcimpl.NewAgentService(&mockGitOps{}, nil, ta, nil)
		token, err := svc.GenerateTerminalToken(ctx, "test-ws", "agent1", "user1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token == "" {
			t.Error("expected non-empty token")
		}
	})
}

// --- GetLog ---

func TestAgentService_GetLog(t *testing.T) {
	ctx := context.Background()
	svc := svcimpl.NewAgentService(&mockGitOps{}, nil, nil, nil)

	t.Run("file not found returns ErrNotFound", func(t *testing.T) {
		_, err := svc.GetLog(ctx, "ws", "no-such-agent", 100, 0)
		requireServiceError(t, err, service.KindNotFound)
	})

	t.Run("line clamping zero becomes default", func(t *testing.T) {
		// Create a temporary log file to exercise the clamping logic.
		configDir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", configDir)
		t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "")

		logDir := filepath.Join(configDir, ".loom", "logs", "test-clamp-ws", "agents")
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		logFile := filepath.Join(logDir, "clamp-agent.log")
		// Write enough lines so the default (200) can be satisfied
		var content string
		for i := 1; i <= 300; i++ {
			content += "line\n"
		}
		if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		result, err := svc.GetLog(ctx, "test-clamp-ws", "clamp-agent", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// lines=0 should be clamped to default (200)
		if len(result.Lines) != 200 {
			t.Errorf("got %d lines, want %d (default)", len(result.Lines), 200)
		}
	})

	t.Run("line clamping exceeds max", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", configDir)
		t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "")

		logDir := filepath.Join(configDir, ".loom", "logs", "test-clampmax-ws", "agents")
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		logFile := filepath.Join(logDir, "clampmax-agent.log")
		// Write enough lines for the max test
		var content string
		for i := 1; i <= 10000+100; i++ {
			content += "line\n"
		}
		if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		result, err := svc.GetLog(ctx, "test-clampmax-ws", "clampmax-agent", 10000+5000, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// lines > 10000 should be clamped to 10000
		if len(result.Lines) != 10000 {
			t.Errorf("got %d lines, want %d (max)", len(result.Lines), 10000)
		}
	})
}

// --- GetDiffStat ---

func TestAgentService_GetDiffStat(t *testing.T) {
	ctx := context.Background()

	t.Run("happy path returns correct values", func(t *testing.T) {
		wt := testWorktree()
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			diffStatFunc: func(worktreePath, fromRef string) ops.DiffStatResult {
				return ops.DiffStatResult{
					FilesChanged: 3,
					LinesAdded:   42,
					LinesRemoved: 17,
				}
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		result, err := svc.GetDiffStat(ctx, "ws", "test-agent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Branch != wt.Branch {
			t.Errorf("Branch = %q, want %q", result.Branch, wt.Branch)
		}
		if result.Added != 42 {
			t.Errorf("Added = %d, want 42", result.Added)
		}
		if result.Removed != 17 {
			t.Errorf("Removed = %d, want 17", result.Removed)
		}
	})

	t.Run("agent not found returns ErrNotFound", func(t *testing.T) {
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return nil, errors.New("not found")
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		_, err := svc.GetDiffStat(ctx, "ws", "missing-agent")
		requireServiceError(t, err, service.KindNotFound)
	})
}

// --- GitPush ---

func TestAgentService_GitPush(t *testing.T) {
	ctx := context.Background()

	t.Run("happy path", func(t *testing.T) {
		wt := testWorktree()
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			pushFunc: func(worktreePath, sourceBranch, targetBranch, remote string) (*ops.GitPushResult, error) {
				return &ops.GitPushResult{
					Success: true,
					Message: "pushed to " + targetBranch,
				}, nil
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		result, err := svc.GitPush(ctx, "ws", "test-agent", "develop")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Success {
			t.Errorf("Success = false, want true")
		}
	})

	t.Run("default target when empty", func(t *testing.T) {
		wt := testWorktree()
		var capturedTarget string
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			pushFunc: func(worktreePath, sourceBranch, targetBranch, remote string) (*ops.GitPushResult, error) {
				capturedTarget = targetBranch
				return &ops.GitPushResult{Success: true, Message: "ok"}, nil
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		_, err := svc.GitPush(ctx, "ws", "test-agent", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedTarget != wt.DefaultBranch {
			t.Errorf("target = %q, want %q (default branch)", capturedTarget, wt.DefaultBranch)
		}
	})
}

// --- GitPushAll ---

func TestAgentService_GitPushAll(t *testing.T) {
	ctx := context.Background()

	t.Run("multiple worktrees with mixed success and failure", func(t *testing.T) {
		gitOps := &mockGitOps{
			listAgentWorktreesFunc: func() ([]ops.AgentWorktree, error) {
				return []ops.AgentWorktree{
					{Name: "agent-ok", Path: "/tmp/wt/ok", Branch: "b-ok", DefaultBranch: "main", Remote: "origin"},
					{Name: "agent-fail", Path: "/tmp/wt/fail", Branch: "b-fail", DefaultBranch: "main", Remote: "origin"},
					{Name: "agent-uptodate", Path: "/tmp/wt/utd", Branch: "b-utd", DefaultBranch: "main", Remote: "origin"},
				}, nil
			},
			pushFunc: func(worktreePath, sourceBranch, targetBranch, remote string) (*ops.GitPushResult, error) {
				switch worktreePath {
				case "/tmp/wt/ok":
					return &ops.GitPushResult{Success: true, Message: "pushed"}, nil
				case "/tmp/wt/fail":
					return nil, errors.New("push failed: conflict")
				case "/tmp/wt/utd":
					return &ops.GitPushResult{Success: true, AlreadyUpToDate: true, Message: "already up to date"}, nil
				}
				return nil, errors.New("unexpected")
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		result, err := svc.GitPushAll(ctx, "ws")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Pushed != 1 {
			t.Errorf("Pushed = %d, want 1", result.Pushed)
		}
		if result.Failed != 1 {
			t.Errorf("Failed = %d, want 1", result.Failed)
		}
		if len(result.Results) != 3 {
			t.Fatalf("len(Results) = %d, want 3", len(result.Results))
		}

		// agent-ok: success
		if !result.Results[0].Success {
			t.Errorf("Results[0].Success = false, want true")
		}
		// agent-fail: error
		if result.Results[1].Success {
			t.Errorf("Results[1].Success = true, want false")
		}
		if result.Results[1].Error == "" {
			t.Error("Results[1].Error should be non-empty")
		}
		// agent-uptodate: success but counted as already-up-to-date (not in pushed count)
		if !result.Results[2].Success {
			t.Errorf("Results[2].Success = false, want true")
		}
	})
}

// --- GitPull ---

func TestAgentService_GitPull(t *testing.T) {
	ctx := context.Background()

	t.Run("happy path", func(t *testing.T) {
		wt := testWorktree()
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			getCurrentBranchFunc: func(worktreePath string) (string, error) {
				return "feature-branch", nil
			},
			pullFunc: func(worktreePath, currentBranch, sourceBranch, remote string) (*ops.GitPullResult, error) {
				return &ops.GitPullResult{Success: true, Message: "pulled from " + sourceBranch}, nil
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		result, err := svc.GitPull(ctx, "ws", "test-agent", "develop")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Success {
			t.Errorf("Success = false, want true")
		}
	})

	t.Run("default source when empty", func(t *testing.T) {
		wt := testWorktree()
		var capturedSource string
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			getCurrentBranchFunc: func(worktreePath string) (string, error) {
				return "feature-branch", nil
			},
			pullFunc: func(worktreePath, currentBranch, sourceBranch, remote string) (*ops.GitPullResult, error) {
				capturedSource = sourceBranch
				return &ops.GitPullResult{Success: true, Message: "ok"}, nil
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		_, err := svc.GitPull(ctx, "ws", "test-agent", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedSource != wt.DefaultBranch {
			t.Errorf("source = %q, want %q (default branch)", capturedSource, wt.DefaultBranch)
		}
	})
}

// --- GitSync ---

func TestAgentService_GitSync(t *testing.T) {
	ctx := context.Background()

	t.Run("happy path", func(t *testing.T) {
		wt := testWorktree()
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			pushFunc: func(worktreePath, sourceBranch, targetBranch, remote string) (*ops.GitPushResult, error) {
				return &ops.GitPushResult{Success: true, Message: "pushed"}, nil
			},
			getCurrentBranchFunc: func(worktreePath string) (string, error) {
				return wt.Branch, nil
			},
			pullFunc: func(worktreePath, currentBranch, sourceBranch, remote string) (*ops.GitPullResult, error) {
				return &ops.GitPullResult{Success: true, Message: "pulled"}, nil
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		result, err := svc.GitSync(ctx, "ws", "test-agent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.PushResult == nil {
			t.Fatal("PushResult is nil")
		}
		if result.PullResult == nil {
			t.Fatal("PullResult is nil")
		}
		if !result.PushResult.Success {
			t.Errorf("PushResult.Success = false, want true")
		}
		if !result.PullResult.Success {
			t.Errorf("PullResult.Success = false, want true")
		}
	})

	t.Run("push conflict returns partial result", func(t *testing.T) {
		wt := testWorktree()
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			pushFunc: func(worktreePath, sourceBranch, targetBranch, remote string) (*ops.GitPushResult, error) {
				return &ops.GitPushResult{
					Success:         false,
					Message:         "conflicts detected",
					ConflictedFiles: []string{"file1.go", "file2.go"},
				}, nil
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		result, err := svc.GitSync(ctx, "ws", "test-agent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.PushResult == nil {
			t.Fatal("PushResult is nil")
		}
		if result.PullResult != nil {
			t.Error("PullResult should be nil when push has conflicts")
		}
		if result.PushResult.Success {
			t.Error("PushResult.Success = true, want false")
		}
		if len(result.PushResult.ConflictedFiles) != 2 {
			t.Errorf("len(ConflictedFiles) = %d, want 2", len(result.PushResult.ConflictedFiles))
		}
	})
}

// --- CreatePR ---

func TestAgentService_CreatePR(t *testing.T) {
	ctx := context.Background()

	t.Run("gh not installed returns ErrUnavailable", func(t *testing.T) {
		gitOps := &mockGitOps{
			checkGhInstalledFunc: func() error {
				return errors.New("gh not found")
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		_, err := svc.CreatePR(ctx, "ws", "test-agent", "main")
		requireServiceError(t, err, service.KindUnavailable)
	})

	t.Run("happy path", func(t *testing.T) {
		wt := testWorktree()
		gitOps := &mockGitOps{
			checkGhInstalledFunc: func() error { return nil },
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			createPRFunc: func(worktreePath, sourceBranch, targetBranch, remote string) (*ops.GitPRResult, error) {
				return &ops.GitPRResult{
					URL:     "https://github.com/test/repo/pull/42",
					Created: true,
				}, nil
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		result, err := svc.CreatePR(ctx, "ws", "test-agent", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Created {
			t.Error("Created = false, want true")
		}
		if result.URL != "https://github.com/test/repo/pull/42" {
			t.Errorf("URL = %q, want %q", result.URL, "https://github.com/test/repo/pull/42")
		}
	})
}

// --- GitReset ---

func TestAgentService_GitReset(t *testing.T) {
	ctx := context.Background()

	t.Run("happy path", func(t *testing.T) {
		wt := testWorktree()
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			resetFunc: func(worktreePath, worktreeName, targetBranch string, force, push bool) (*ops.GitResetResult, error) {
				return &ops.GitResetResult{
					Success:        true,
					Message:        "reset to " + targetBranch,
					PreviousBranch: "old-branch",
					Pushed:         push,
				}, nil
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		result, err := svc.GitReset(ctx, "ws", "test-agent", "develop", true, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Success {
			t.Error("Success = false, want true")
		}
		if !result.Pushed {
			t.Error("Pushed = false, want true")
		}
	})

	t.Run("locked error passes through", func(t *testing.T) {
		wt := testWorktree()
		lockedErr := &ops.GitResetLockedError{
			AgentName: "test-agent",
			PID:       1234,
			Duration:  "5m",
			TaskID:    "task-1",
		}
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			resetFunc: func(worktreePath, worktreeName, targetBranch string, force, push bool) (*ops.GitResetResult, error) {
				return nil, lockedErr
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		_, err := svc.GitReset(ctx, "ws", "test-agent", "main", false, false)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var gotLocked *ops.GitResetLockedError
		if !errors.As(err, &gotLocked) {
			t.Fatalf("expected *ops.GitResetLockedError, got %T: %v", err, err)
		}
		if gotLocked.PID != 1234 {
			t.Errorf("PID = %d, want 1234", gotLocked.PID)
		}
	})

	t.Run("default branch when empty", func(t *testing.T) {
		wt := testWorktree()
		var capturedBranch string
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			resetFunc: func(worktreePath, worktreeName, targetBranch string, force, push bool) (*ops.GitResetResult, error) {
				capturedBranch = targetBranch
				return &ops.GitResetResult{Success: true, Message: "ok"}, nil
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		_, err := svc.GitReset(ctx, "ws", "test-agent", "", false, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedBranch != wt.DefaultBranch {
			t.Errorf("branch = %q, want %q (default)", capturedBranch, wt.DefaultBranch)
		}
	})
}

// --- GitStatus ---

func TestAgentService_GitStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("happy path", func(t *testing.T) {
		wt := testWorktree()
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			statusFunc: func(worktreePath, targetBranch string) (*ops.GitStatusResult, error) {
				return &ops.GitStatusResult{
					Branch:       "feature",
					TargetBranch: "main",
					IsClean:      false,
					Ahead:        2,
					Behind:       1,
					ChangedFiles: []string{"a.go", "b.go"},
				}, nil
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		result, err := svc.GitStatus(ctx, "ws", "test-agent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Branch != "feature" {
			t.Errorf("Branch = %q, want %q", result.Branch, "feature")
		}
		if result.Ahead != 2 {
			t.Errorf("Ahead = %d, want 2", result.Ahead)
		}
		if len(result.ChangedFiles) != 2 {
			t.Errorf("len(ChangedFiles) = %d, want 2", len(result.ChangedFiles))
		}
	})

	t.Run("error propagation", func(t *testing.T) {
		wt := testWorktree()
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			statusFunc: func(worktreePath, targetBranch string) (*ops.GitStatusResult, error) {
				return nil, errors.New("git status failed")
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		_, err := svc.GitStatus(ctx, "ws", "test-agent")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		// The error is wrapped with fmt.Errorf, not a ServiceError
		if !errors.Is(err, errors.Unwrap(err)) && err.Error() == "" {
			t.Error("expected non-empty error message")
		}
	})
}

// --- SetTargetBranch ---

func TestAgentService_SetTargetBranch(t *testing.T) {
	ctx := context.Background()

	t.Run("non-workspace mode returns ErrValidation", func(t *testing.T) {
		wt := testWorktree()
		wt.IsWorkspace = false
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		err := svc.SetTargetBranch(ctx, "ws", "test-agent", "develop")
		requireServiceError(t, err, service.KindValidation)
	})

	t.Run("happy path", func(t *testing.T) {
		wt := testWorktree()
		wt.IsWorkspace = true
		var capturedBranch string
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*ops.AgentWorktree, error) {
				return wt, nil
			},
			setRepoDefaultFunc: func(repoName, branch string) error {
				capturedBranch = branch
				return nil
			},
		}
		svc := svcimpl.NewAgentService(gitOps, nil, nil, nil)

		err := svc.SetTargetBranch(ctx, "ws", "test-agent", "develop")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedBranch != "develop" {
			t.Errorf("branch = %q, want %q", capturedBranch, "develop")
		}
	})
}
