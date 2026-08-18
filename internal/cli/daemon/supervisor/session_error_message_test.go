package supervisor

import (
	"strings"
	"sync"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

func TestAgentErrorMessage(t *testing.T) {
	t.Run("nil LastError yields empty string", func(t *testing.T) {
		ap := &AgentProcess{}
		if got := agentErrorMessage(ap); got != "" {
			t.Fatalf("agentErrorMessage() = %q, want empty", got)
		}
	})

	t.Run("classified error yields its message", func(t *testing.T) {
		ap := &AgentProcess{LastError: &agenterr.AgentError{
			Class:   agenterr.OutcomeFromHarness(wrapper.ErrRateLimited),
			Message: "rate limit exceeded",
		}}
		if got := agentErrorMessage(ap); got != "rate limit exceeded" {
			t.Fatalf("agentErrorMessage() = %q, want %q", got, "rate limit exceeded")
		}
	})

	// The suite runs under -race: the read must hold ap.Mu like every other
	// LastError reader does.
	t.Run("concurrent reads and writes are race-free", func(t *testing.T) {
		ap := &AgentProcess{}
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				ap.Mu.Lock()
				ap.LastError = &agenterr.AgentError{Message: "boom"}
				ap.Mu.Unlock()
			}()
			go func() {
				defer wg.Done()
				_ = agentErrorMessage(ap)
			}()
		}
		wg.Wait()
	})
}

func TestAgentSessionSummary(t *testing.T) {
	if got := agentSessionSummary(domain.AgentSessionFailed, ""); got != nil {
		t.Fatalf("empty message summary = %v, want nil", got)
	}
	if got := agentSessionSummary(domain.AgentSessionCompleted, "stale boom"); got != nil {
		t.Fatalf("completed-status summary = %v, want nil", got)
	}
	got := agentSessionSummary(domain.AgentSessionFailed, "boom")
	if got == nil || *got != "boom" {
		t.Fatalf("failed-status summary = %v, want \"boom\"", got)
	}
}

func TestCompleteControlPlaneAgentSessionCarriesErrorMessage(t *testing.T) {
	tests := []struct {
		name        string
		exitCode    int
		errMessage  string
		wantSummary string
	}{
		{name: "failed run publishes the message", exitCode: 7, errMessage: "rate limit exceeded", wantSummary: "rate limit exceeded"},
		{name: "clean run leaves summary unset", exitCode: 0, errMessage: "stale boom", wantSummary: ""},
		{name: "failed run without a message stays blank", exitCode: 7, errMessage: "", wantSummary: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := memstore.New()
			s := newControlPlaneTestSupervisor(st)
			ap := &AgentProcess{
				Entry:        cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task"},
				RoleConfig:   cfgpkg.RoleConfig{Backend: "claude"},
				WorktreePath: t.TempDir(),
			}
			s.createAgentSession(ap, "epic-1")
			sessionID := ap.AgentSessionID
			if sessionID == "" {
				t.Fatal("AgentSessionID was not set")
			}
			s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
				sessionID:  sessionID,
				leaseID:    ap.AgentLeaseID,
				leaseToken: ap.AgentLeaseToken,
				exitCode:   tt.exitCode,
				errClass:   "RateLimited",
				errMessage: tt.errMessage,
				taskID:     "task-1",
			})
			session, err := st.AgentSessions().Get(t.Context(), "WS", sessionID)
			if err != nil {
				t.Fatalf("get completed agent session: %v", err)
			}
			if session.Summary != tt.wantSummary {
				t.Fatalf("summary = %q, want %q", session.Summary, tt.wantSummary)
			}
		})
	}
}

func TestCompleteBackendUnavailableCleanupSetsErrorMessage(t *testing.T) {
	st := memstore.New()
	s := newControlPlaneTestSupervisor(st)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task"},
		RoleConfig:   cfgpkg.RoleConfig{Backend: "claude"},
		WorktreePath: t.TempDir(),
	}
	s.createAgentSession(ap, "epic-1")
	sessionID := ap.AgentSessionID
	s.completeBackendUnavailableCleanup(ap)

	session, err := st.AgentSessions().Get(t.Context(), "WS", sessionID)
	if err != nil {
		t.Fatalf("get completed agent session: %v", err)
	}
	if session.ErrorClass != "backend_unavailable" {
		t.Fatalf("error class = %q, want backend_unavailable", session.ErrorClass)
	}
	if !strings.Contains(session.Summary, "backend binary unavailable") {
		t.Fatalf("summary = %q, want the backend-unavailable message", session.Summary)
	}
}

// TestFinalizeLocalSessionWritesLastError pins the on-disk half of the thread:
// the classified message reaches SessionMetadata.LastError via the shared
// sessionfinalize plumbing.
func TestFinalizeLocalSessionWritesLastError(t *testing.T) {
	store, err := sessions.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{AgentName: "coder", Backend: "claude", AttemptNum: 1})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task"},
		WorktreePath: t.TempDir(),
	}
	finalizeLocalSession(sess, ap, "", "task-1", 1, "RateLimited", "rate limit exceeded", leafUsage{})

	meta, err := store.LoadMetadata(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.ErrorClass != "RateLimited" || meta.LastError != "rate limit exceeded" {
		t.Fatalf("error class/message = %q/%q, want RateLimited/rate limit exceeded", meta.ErrorClass, meta.LastError)
	}
}
