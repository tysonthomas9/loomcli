package leadcontrol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

func TestFreshCodexTUIArgsRemainUnchanged(t *testing.T) {
	cfg := CodexLeadRuntimeConfig{WorkDir: "/repo", Prompt: "lead prompt"}
	got := freshCodexTUIArgs(cfg, "ws://127.0.0.1:1234")
	want := []string{
		"--remote", "ws://127.0.0.1:1234",
		"--no-alt-screen",
		"--dangerously-bypass-approvals-and-sandbox",
		"-C", "/repo",
		"lead prompt",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("freshCodexTUIArgs() = %#v, want %#v", got, want)
	}
}

func TestResumeCodexTUIArgsUseExplicitThreadAndReanchorPrompt(t *testing.T) {
	cfg := CodexLeadRuntimeConfig{WorkDir: "/repo", Prompt: "full lead prompt"}
	got := resumeCodexTUIArgs(cfg, "ws://127.0.0.1:1234", "thread-1")
	want := []string{
		"resume", "thread-1",
		"--remote", "ws://127.0.0.1:1234",
		"--no-alt-screen",
		"--dangerously-bypass-approvals-and-sandbox",
		"-C", "/repo",
		codexResumeReanchorPrompt,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("resumeCodexTUIArgs() = %#v, want %#v", got, want)
	}
}

func TestDecideCodexResume(t *testing.T) {
	tests := []struct {
		name        string
		prior       CodexRuntimeMetadata
		readThread  func(context.Context, string) (*CodexThread, error)
		wantResume  bool
		wantAttempt bool
		wantClear   bool
	}{
		{
			name:  "no thread id launches fresh",
			prior: CodexRuntimeMetadata{},
		},
		{
			name:  "matching persisted thread resumes",
			prior: CodexRuntimeMetadata{ThreadID: "thread-1"},
			readThread: func(context.Context, string) (*CodexThread, error) {
				return &CodexThread{ID: "thread-1", Cwd: "/repo"}, nil
			},
			wantResume:  true,
			wantAttempt: true,
		},
		{
			name:  "read error launches fresh and clears stale id",
			prior: CodexRuntimeMetadata{ThreadID: "thread-1"},
			readThread: func(context.Context, string) (*CodexThread, error) {
				return nil, errors.New("thread unavailable")
			},
			wantAttempt: true,
			wantClear:   true,
		},
		{
			name:  "cwd mismatch launches fresh",
			prior: CodexRuntimeMetadata{ThreadID: "thread-1"},
			readThread: func(context.Context, string) (*CodexThread, error) {
				return &CodexThread{ID: "thread-1", Cwd: "/other"}, nil
			},
			wantAttempt: true,
			wantClear:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readThread := tt.readThread
			if readThread == nil {
				readThread = func(context.Context, string) (*CodexThread, error) {
					t.Fatal("ReadThread called without a prior thread id")
					return nil, nil
				}
			}
			decision := decideCodexResume(context.Background(), CodexLeadRuntimeConfig{
				Store:          memstore.New(),
				SessionID:      "lead-session",
				WorkDir:        "/repo",
				ResumeEligible: true,
			}, tt.prior, readThread)
			if decision.Resume != tt.wantResume || decision.Attempted != tt.wantAttempt || decision.ClearThreadID != tt.wantClear {
				t.Fatalf("decideCodexResume() = %+v, want resume=%v attempted=%v clear=%v", decision, tt.wantResume, tt.wantAttempt, tt.wantClear)
			}
			if tt.wantAttempt && !tt.wantResume && decision.Reason == "" {
				t.Fatal("fresh fallback did not include a reason")
			}
		})
	}
}

func TestDuplicateLiveCodexRuntimeIsRefused(t *testing.T) {
	runtime := CodexRuntimeMetadata{
		Endpoint:   "ws://127.0.0.1:1234",
		PID:        42,
		Status:     RuntimeStatusIdle,
		Controlled: true,
	}
	if !duplicateCodexRuntimeLive(runtime, true) {
		t.Fatal("live controlled runtime was not recognized as a duplicate")
	}
	if duplicateCodexRuntimeLive(runtime, false) {
		t.Fatal("dead prior runtime was recognized as a duplicate")
	}
}

func TestNewestCodexResumeReplacementExcludesRequestedThread(t *testing.T) {
	launchedAt := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	threads := []CodexThread{
		{
			ID:          "thread-1",
			Cwd:         "/repo",
			CreatedAtMS: float64(launchedAt.Add(-time.Hour).UnixMilli()),
			UpdatedAtMS: float64(launchedAt.Add(time.Second).UnixMilli()),
		},
		{
			ID:          "thread-2",
			Cwd:         "/repo",
			CreatedAtMS: float64(launchedAt.Add(time.Second).UnixMilli()),
		},
	}

	got := newestCodexResumeReplacement(threads, "thread-1", "/repo", launchedAt)
	if got == nil || got.ID != "thread-2" {
		t.Fatalf("newestCodexResumeReplacement() = %+v, want thread-2", got)
	}
}

func TestCodexResumedThreadRequiresPostLaunchEvidence(t *testing.T) {
	launchedAt := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	prior := CodexThread{
		ID:          "thread-1",
		Cwd:         "/repo",
		UpdatedAtMS: float64(launchedAt.Add(-time.Minute).UnixMilli()),
		Status:      CodexThreadStatus{Type: "idle"},
	}
	if codexResumedThreadHasPostLaunchEvidence(prior, prior, launchedAt) {
		t.Fatal("unchanged requested thread counted as post-launch evidence")
	}
	updated := prior
	updated.UpdatedAtMS = float64(launchedAt.Add(time.Second).UnixMilli())
	if !codexResumedThreadHasPostLaunchEvidence(prior, updated, launchedAt) {
		t.Fatal("updated requested thread was not recognized as post-launch evidence")
	}
}

func TestNewestCodexThreadWaitsForThreadCreatedAfterRuntimeStart(t *testing.T) {
	startedAt := time.Date(2026, 5, 17, 6, 5, 36, 0, time.UTC)
	threads := []CodexThread{
		{
			ID:          "old-lead-thread",
			Cwd:         "/repo",
			CreatedAtMS: float64(startedAt.Add(-3 * time.Minute).UnixMilli()),
			UpdatedAtMS: float64(startedAt.Add(-1 * time.Second).UnixMilli()),
		},
		{
			ID:          "new-lead-thread",
			Cwd:         "/repo",
			CreatedAtMS: float64(startedAt.Add(500 * time.Millisecond).UnixMilli()),
			UpdatedAtMS: float64(startedAt.Add(2 * time.Second).UnixMilli()),
		},
	}

	got := newestCodexThread(threads, "/repo", startedAt)
	if got == nil || got.ID != "new-lead-thread" {
		t.Fatalf("newestCodexThread() = %+v, want new-lead-thread", got)
	}
}

func TestNewestCodexThreadReturnsNilUntilFreshLeadThreadExists(t *testing.T) {
	startedAt := time.Date(2026, 5, 17, 6, 5, 36, 0, time.UTC)
	threads := []CodexThread{{
		ID:          "old-lead-thread",
		Cwd:         "/repo",
		CreatedAtMS: float64(startedAt.Add(-3 * time.Minute).UnixMilli()),
		UpdatedAtMS: float64(startedAt.Add(5 * time.Second).UnixMilli()),
	}}

	got := newestCodexThread(threads, "/repo", startedAt)
	if got != nil {
		t.Fatalf("newestCodexThread() = %+v, want nil before fresh lead thread exists", got)
	}
}

func TestCodexAppServerTimeoutErrorIncludesProbeAndLogTail(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "app-server.log")
	logBody := strings.Repeat("x", int(codexAppServerLogTailBytes)+32) + "\nstartup detail\n"
	if err := os.WriteFile(logPath, []byte(logBody), 0600); err != nil {
		t.Fatalf("write app-server log: %v", err)
	}

	err := codexAppServerTimeoutError(
		"ws://127.0.0.1:62085",
		5*time.Second,
		errors.New("connection refused"),
		logPath,
	)
	got := err.Error()
	for _, want := range []string{
		"codex app-server did not become ready at ws://127.0.0.1:62085 within 5s",
		"last readiness probe: connection refused",
		"app-server log tail:",
		"startup detail",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("timeout error missing %q:\n%s", want, got)
		}
	}
}

func TestCodexAppServerTimeoutErrorOmitsMissingLogTail(t *testing.T) {
	t.Parallel()

	err := codexAppServerTimeoutError(
		"ws://127.0.0.1:62085",
		5*time.Second,
		nil,
		filepath.Join(t.TempDir(), "missing.log"),
	)
	got := err.Error()
	if strings.Contains(got, "app-server log tail:") {
		t.Fatalf("timeout error included missing log tail:\n%s", got)
	}
	if strings.Contains(got, "last readiness probe:") {
		t.Fatalf("timeout error included missing probe error:\n%s", got)
	}
}
