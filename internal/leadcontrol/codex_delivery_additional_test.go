package leadcontrol

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestDeliverCurrentAssignmentToCodexAdditionalBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("no assignment", func(t *testing.T) {
		result, err := DeliverCurrentAssignmentToCodex(ctx, memstore.New(), "WS", "nova")
		if err != nil {
			t.Fatalf("DeliverCurrentAssignmentToCodex: %v", err)
		}
		if result.State != DeliveryStateNone {
			t.Fatalf("state = %q, want none", result.State)
		}
	})

	t.Run("no orchestration session", func(t *testing.T) {
		st := memstore.New()
		if _, err := st.Agents().Create(ctx, store.AgentCreate{
			WorkspaceKey: "WS",
			Name:         "nova",
			RoleName:     "lead",
			Backend:      "codex",
			Parent:       "EPIC-1",
		}); err != nil {
			t.Fatalf("create lead: %v", err)
		}
		result, err := DeliverCurrentAssignmentToCodex(ctx, st, "WS", "nova")
		if err != nil {
			t.Fatalf("DeliverCurrentAssignmentToCodex: %v", err)
		}
		if result.State != DeliveryStatePending || !strings.Contains(result.Reason, "no orchestration session") {
			t.Fatalf("result = %+v, want pending no orchestration session", result)
		}
	})

	t.Run("already delivered", func(t *testing.T) {
		st := memstore.New()
		createAssignedLeadSession(t, st, "already-delivered", nil)
		assignment, err := epicrunner.LoadLeadAssignmentContext(ctx, st, "WS", "nova")
		if err != nil {
			t.Fatalf("LoadLeadAssignmentContext: %v", err)
		}
		if err := MarkAssignmentDelivered(ctx, st, "WS", "lead-session", assignment.EpicID, assignment.AssignmentVersion); err != nil {
			t.Fatalf("MarkAssignmentDelivered: %v", err)
		}
		orig := dialCodexAppServerClient
		dialCodexAppServerClient = func(context.Context, string) (codexAppServerClient, error) {
			t.Fatal("dial should not be called for an already delivered assignment")
			return nil, nil
		}
		t.Cleanup(func() { dialCodexAppServerClient = orig })
		result, err := DeliverCurrentAssignmentToCodex(ctx, st, "WS", "nova")
		if err != nil {
			t.Fatalf("DeliverCurrentAssignmentToCodex: %v", err)
		}
		if result.State != DeliveryStateDelivered {
			t.Fatalf("state = %q, want delivered", result.State)
		}
	})
}

func TestDeliverCurrentAssignmentToCodexRecordsClientFailures(t *testing.T) {
	for _, tt := range []struct {
		name      string
		install   func(*testing.T)
		wantError string
	}{
		{
			name: "dial failure",
			install: func(t *testing.T) {
				t.Helper()
				orig := dialCodexAppServerClient
				dialCodexAppServerClient = func(context.Context, string) (codexAppServerClient, error) {
					return nil, errors.New("dial failed")
				}
				t.Cleanup(func() { dialCodexAppServerClient = orig })
			},
			wantError: "dial failed",
		},
		{
			name: "read failure",
			install: func(t *testing.T) {
				t.Helper()
				installErrorCodexClient(t, errorCodexClient{readErr: errors.New("read failed")})
			},
			wantError: "read failed",
		},
		{
			name: "start failure",
			install: func(t *testing.T) {
				t.Helper()
				installErrorCodexClient(t, errorCodexClient{
					status:   CodexThreadStatus{Type: "idle"},
					startErr: errors.New("start failed"),
				})
			},
			wantError: "start failed",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := memstore.New()
			createAssignedLeadSession(t, st, tt.name, nil)
			setCodexRuntimeMetadata(t, st, "WS", "lead-session", "ws://codex.test", "thread-1")
			tt.install(t)

			result, err := DeliverCurrentAssignmentToCodex(context.Background(), st, "WS", "nova")
			if err != nil {
				t.Fatalf("DeliverCurrentAssignmentToCodex: %v", err)
			}
			if result.State != DeliveryStatePending || !strings.Contains(result.Reason, tt.wantError) {
				t.Fatalf("result = %+v, want pending reason %q", result, tt.wantError)
			}
			session, err := st.AgentSessions().Get(context.Background(), "WS", "lead-session")
			if err != nil {
				t.Fatalf("get session: %v", err)
			}
			if !strings.Contains(session.Metadata[MetadataDeliveryError], tt.wantError) {
				t.Fatalf("delivery error metadata = %#v, want %q", session.Metadata, tt.wantError)
			}
		})
	}
}

func TestCodexRuntimeMetadataUpdateAndDeliveryMarkerBranches(t *testing.T) {
	ctx := context.Background()
	if got := RuntimeMetadataFromSession(nil); got != (CodexRuntimeMetadata{}) {
		t.Fatalf("nil RuntimeMetadataFromSession = %+v", got)
	}
	if err := UpdateCodexRuntimeMetadata(ctx, nil, "WS", "sess", CodexRuntimeMetadata{}); err != nil {
		t.Fatalf("UpdateCodexRuntimeMetadata nil store: %v", err)
	}
	if err := MarkAssignmentDelivered(ctx, nil, "WS", "sess", "EPIC", "v1"); err != nil {
		t.Fatalf("MarkAssignmentDelivered nil store: %v", err)
	}
	if err := MarkAssignmentDeliveryAttempt(ctx, nil, "WS", "sess", "boom"); err != nil {
		t.Fatalf("MarkAssignmentDeliveryAttempt nil store: %v", err)
	}

	st := memstore.New()
	if err := UpdateCodexRuntimeMetadata(ctx, st, "", "", CodexRuntimeMetadata{}); err != nil {
		t.Fatalf("UpdateCodexRuntimeMetadata empty identifiers: %v", err)
	}
	if err := UpdateCodexRuntimeMetadata(ctx, st, "WS", "missing", CodexRuntimeMetadata{Endpoint: "ws://x"}); err != nil {
		t.Fatalf("UpdateCodexRuntimeMetadata missing session: %v", err)
	}
	if err := MarkAssignmentDelivered(ctx, st, "", "", "EPIC", "v1"); err != nil {
		t.Fatalf("MarkAssignmentDelivered empty identifiers: %v", err)
	}
	if err := MarkAssignmentDeliveryAttempt(ctx, st, "WS", "missing", "boom"); err != nil {
		t.Fatalf("MarkAssignmentDeliveryAttempt missing session: %v", err)
	}

	createAssignedLeadSession(t, st, "metadata", nil)
	runtime := CodexRuntimeMetadata{
		Endpoint:    "ws://codex.test",
		ThreadID:    "thread-1",
		RuntimeHome: "/runtime",
		SQLiteHome:  "/runtime/sqlite",
		PID:         1234,
		Status:      RuntimeStatusIdle,
		Controlled:  true,
	}
	if err := UpdateCodexRuntimeMetadata(ctx, st, "WS", "lead-session", runtime); err != nil {
		t.Fatalf("UpdateCodexRuntimeMetadata: %v", err)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	got := RuntimeMetadataFromSession(session)
	if got.Endpoint != runtime.Endpoint || got.ThreadID != runtime.ThreadID || got.PID != runtime.PID || !got.Controlled {
		t.Fatalf("runtime metadata = %+v, want %+v", got, runtime)
	}
	if session.Metadata[MetadataRuntimeStatusUpdated] == "" {
		t.Fatalf("status timestamp missing: %#v", session.Metadata)
	}
	if err := MarkAssignmentDeliveryAttempt(ctx, st, "WS", "lead-session", "  "); err != nil {
		t.Fatalf("MarkAssignmentDeliveryAttempt blank message: %v", err)
	}
	if session, err = st.AgentSessions().Get(ctx, "WS", "lead-session"); err != nil {
		t.Fatalf("get session after attempt: %v", err)
	}
	if session.Metadata[MetadataDeliveryAttemptedAt] == "" {
		t.Fatalf("attempt timestamp missing: %#v", session.Metadata)
	}
}

func TestDiscoverCodexLeadThreadReturnsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	discoverCodexLeadThread(ctx, CodexLeadRuntimeConfig{}, CodexRuntimeMetadata{}, time.Now())
}

func TestCodexRuntimeHelperBranches(t *testing.T) {
	runtimeHome, sqliteHome := codexLeadRuntimeDirs(CodexLeadRuntimeConfig{
		Workspace: "  !!! ",
		LeadName:  " ",
		SessionID: "",
	})
	if !strings.Contains(runtimeHome, "workspace") || !strings.Contains(runtimeHome, "lead") || !strings.Contains(runtimeHome, "session") {
		t.Fatalf("runtimeHome = %q, want fallback path parts", runtimeHome)
	}
	if !strings.HasSuffix(sqliteHome, "sqlite") {
		t.Fatalf("sqliteHome = %q, want sqlite suffix", sqliteHome)
	}
	if got := sanitizeRuntimePathPart("Team A/Lead_1."); got != "team-a-lead_1." {
		t.Fatalf("sanitizeRuntimePathPart = %q", got)
	}
	if endpoint, err := freeLoopbackWSEndpoint(); err != nil || !strings.HasPrefix(endpoint, "ws://127.0.0.1:") {
		t.Fatalf("freeLoopbackWSEndpoint = %q, %v", endpoint, err)
	}

	now := time.Now().UTC()
	threads := []CodexThread{
		{ID: "", Cwd: "/repo", CreatedAt: float64(now.Unix()), UpdatedAt: float64(now.Add(5 * time.Second).Unix())},
		{ID: "wrong-cwd", Cwd: "/other", CreatedAt: float64(now.Unix()), UpdatedAt: float64(now.Add(10 * time.Second).Unix())},
		{ID: "too-old", Cwd: "/repo", CreatedAt: float64(now.Add(-time.Hour).Unix()), UpdatedAt: float64(now.Add(time.Hour).Unix())},
		{ID: "older", Cwd: "/repo", CreatedAtMS: float64(now.UnixMilli()), UpdatedAtMS: float64(now.Add(time.Second).UnixMilli())},
		{ID: "newest", Cwd: "/repo", CreatedAt: float64(now.Unix()), UpdatedAt: float64(now.Add(2 * time.Second).Unix())},
	}
	if got := newestCodexThread(threads, "/repo", now); got == nil || got.ID != "newest" {
		t.Fatalf("newestCodexThread = %+v, want newest", got)
	}
	if got := newestCodexThread(threads, "/missing", now); got != nil {
		t.Fatalf("newestCodexThread missing cwd = %+v, want nil", got)
	}
	if !threadCreatedAt(CodexThread{CreatedAt: float64(now.Unix())}).Equal(time.Unix(now.Unix(), 0).UTC()) {
		t.Fatal("threadCreatedAt did not fall back to CreatedAt seconds")
	}
	if !threadSortTime(CodexThread{}).IsZero() {
		t.Fatal("threadSortTime empty thread should be zero")
	}

	appErr := make(chan error, 1)
	appErr <- nil
	close(appErr)
	if err := waitForCodexAppServer(context.Background(), "ws://127.0.0.1:1", appErr); err == nil || !strings.Contains(err.Error(), "exited before ready") {
		t.Fatalf("waitForCodexAppServer nil app exit err = %v", err)
	}
	if err := stopCodexAppServer(nil, nil, func() {}); err != nil {
		t.Fatalf("stopCodexAppServer nil cmd = %v", err)
	}
}

type errorCodexClient struct {
	status   CodexThreadStatus
	readErr  error
	startErr error
}

func installErrorCodexClient(t *testing.T, fake errorCodexClient) {
	t.Helper()
	if fake.status.Type == "" {
		fake.status = CodexThreadStatus{Type: "idle"}
	}
	orig := dialCodexAppServerClient
	dialCodexAppServerClient = func(context.Context, string) (codexAppServerClient, error) {
		return &fake, nil
	}
	t.Cleanup(func() { dialCodexAppServerClient = orig })
}

func (f *errorCodexClient) Close(string) error { return nil }

func (f *errorCodexClient) ListThreads(context.Context, string, int) ([]CodexThread, error) {
	return []CodexThread{{ID: "thread-1", Cwd: "/repo", Status: f.status}}, nil
}

func (f *errorCodexClient) ReadThread(context.Context, string) (*CodexThread, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return &CodexThread{ID: "thread-1", Cwd: "/repo", Status: f.status}, nil
}

func (f *errorCodexClient) StartTurn(context.Context, string, string) error {
	return f.startErr
}

func TestCodexRuntimeMetadataFromSessionIgnoresInvalidPID(t *testing.T) {
	session := &domain.AgentSession{Metadata: map[string]string{
		MetadataCodexPID:          "not-a-number",
		MetadataRuntimeControlled: "TRUE",
	}}
	if got := RuntimeMetadataFromSession(session); got.PID != 0 || !got.Controlled {
		t.Fatalf("metadata = %+v, want zero PID and controlled true", got)
	}
}
