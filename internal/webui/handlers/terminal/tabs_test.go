package terminal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	"github.com/tysonthomas9/loomcli/internal/webui/worktreegroups"
)

const tabWorktreeTestWS = "E2E"

type recordingTerminalService struct {
	meta *tabmeta.TabMetadata
}

func (s *recordingTerminalService) GenerateToken(_ context.Context, _, _, _ string) (string, error) {
	return "token", nil
}

func (s *recordingTerminalService) ListTabs(_ context.Context, _ string) ([]tabmeta.TabMetadata, error) {
	return nil, nil
}

func (s *recordingTerminalService) GetTab(_ context.Context, _, _ string) (*tabmeta.TabMetadata, error) {
	return nil, nil
}

func (s *recordingTerminalService) PatchTab(_ context.Context, _, _ string, _ map[string]string) (*service.PatchTabResult, error) {
	return nil, nil
}

func (s *recordingTerminalService) PutTab(_ context.Context, _ string, meta *tabmeta.TabMetadata) error {
	copied := *meta
	if meta.Launch != nil {
		launch := *meta.Launch
		copied.Launch = &launch
	}
	s.meta = &copied
	return nil
}

func (s *recordingTerminalService) DeleteTab(_ context.Context, _, _ string) error {
	return nil
}

func (s *recordingTerminalService) ListSessionsByIssue(_ context.Context) (map[string][]string, error) {
	return nil, nil
}

func (s *recordingTerminalService) GetTerminalState(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (s *recordingTerminalService) PatchTerminalState(_ context.Context, _, _ string) error {
	return nil
}

func (s *recordingTerminalService) StartSetup(_ context.Context, _ string, req service.TerminalSetupRequest) (*service.TerminalSetupResult, error) {
	return &service.TerminalSetupResult{Backend: req.Backend, Action: req.Action}, nil
}

type tabPutHarness struct {
	ctx             context.Context
	wsPath          string
	store           store.Store
	worktreeGroups  *worktreegroups.Store
	terminalService *recordingTerminalService
}

func newTabPutHarness(t *testing.T) *tabPutHarness {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	ctx := context.Background()
	wsPath := filepath.Join(t.TempDir(), "workspace")
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: tabWorktreeTestWS, Name: "E2E"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			tabWorktreeTestWS: {Path: wsPath},
		},
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	return &tabPutHarness{
		ctx:             ctx,
		wsPath:          wsPath,
		store:           st,
		worktreeGroups:  worktreegroups.NewStore(rdb, nil),
		terminalService: &recordingTerminalService{},
	}
}

func putTab(t *testing.T, h *tabPutHarness, session, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/E2E/terminal/tabs/"+session, strings.NewReader(body))
	req.SetPathValue("session", session)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), tabWorktreeTestWS))
	w := httptest.NewRecorder()
	HandlePutTerminalTab(h.terminalService, h.store, h.worktreeGroups).ServeHTTP(w, req)
	return w
}

func TestHandlePutTerminalTabDefaultWorktreeGroup(t *testing.T) {
	for _, body := range []string{
		`{"label":"Default","sort_order":1,"notes":"","pinned":false}`,
		`{"label":"Default","sort_order":1,"notes":"","pinned":false,"worktree_group_id":"__workspace__"}`,
	} {
		h := newTabPutHarness(t)
		w := putTab(t, h, "lead-shell-1", body)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
		}
		if h.terminalService.meta.WorktreeGroupID != tabmeta.DefaultWorktreeGroupID {
			t.Fatalf("WorktreeGroupID = %q, want %q", h.terminalService.meta.WorktreeGroupID, tabmeta.DefaultWorktreeGroupID)
		}
		if h.terminalService.meta.Launch != nil {
			t.Fatalf("Launch = %+v, want nil", h.terminalService.meta.Launch)
		}
	}
}

func TestHandlePutTerminalTabGroupRecomputesCwd(t *testing.T) {
	h := newTabPutHarness(t)
	const groupID = "group-id"
	const groupName = "feature-tabs"
	staleRoot := filepath.Join(t.TempDir(), "stale-root")
	if err := h.worktreeGroups.Add(h.ctx, tabWorktreeTestWS, worktreegroups.TerminalWorktreeGroup{
		ID:        groupID,
		Name:      groupName,
		Root:      staleRoot,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add group: %v", err)
	}

	w := putTab(t, h, "copy-2", `{"label":"Group","sort_order":1,"notes":"","pinned":false,"worktree_group_id":"group-id"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	got := h.terminalService.meta
	if got.WorktreeGroupID != groupID {
		t.Fatalf("WorktreeGroupID = %q, want %q", got.WorktreeGroupID, groupID)
	}
	if got.Launch == nil {
		t.Fatal("Launch is nil, want group launch")
	}
	wantCwd, err := localworkspace.TerminalGroupRootPath(h.wsPath, groupName)
	if err != nil {
		t.Fatalf("TerminalGroupRootPath: %v", err)
	}
	if got.Launch.Cwd != wantCwd {
		t.Fatalf("Launch.Cwd = %q, want %q", got.Launch.Cwd, wantCwd)
	}
	if got.Launch.Cwd == staleRoot {
		t.Fatalf("Launch.Cwd used stale stored root %q", staleRoot)
	}
	if len(got.Launch.Argv) != 0 {
		t.Fatalf("Launch.Argv = %v, want nil/empty for duplicated-tab session", got.Launch.Argv)
	}
}

func TestHandlePutTerminalTabUnknownGroupFallsBack(t *testing.T) {
	h := newTabPutHarness(t)
	w := putTab(t, h, "copy-3", `{"label":"Fallback","sort_order":1,"notes":"","pinned":false,"worktree_group_id":"missing-group"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	got := h.terminalService.meta
	if got.WorktreeGroupID != tabmeta.DefaultWorktreeGroupID {
		t.Fatalf("WorktreeGroupID = %q, want %q", got.WorktreeGroupID, tabmeta.DefaultWorktreeGroupID)
	}
	if got.Launch != nil {
		t.Fatalf("Launch = %+v, want nil", got.Launch)
	}
}
