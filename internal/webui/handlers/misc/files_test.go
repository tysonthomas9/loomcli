package misc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// mockFileOps implements Source Control's private FileMechanics seam for
// scoped file handler tests.
type mockFileOps struct {
	resolveFunc       func(name string) (*sourcecontrol.Worktree, error)
	resolveWsRootFunc func() (string, error)
	resolveWsDataFunc func() (*sourcecontrol.WorkspaceTopology, error)
	repairFunc        func(workspaceID, scope, target, repo string, force bool) (sourcecontrol.RepairResult, error)
}

func (m *mockFileOps) ResolveAgentWorktree(_ string, name string) (*sourcecontrol.Worktree, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(name)
	}
	return nil, errors.New("not found")
}

func (m *mockFileOps) ResolveAgentWorktreeForRepo(workspaceID, name, _ string) (*sourcecontrol.Worktree, error) {
	return m.ResolveAgentWorktree(workspaceID, name)
}

func (m *mockFileOps) ResolveWorkspaceRoot(_ string) (string, error) {
	if m.resolveWsRootFunc != nil {
		return m.resolveWsRootFunc()
	}
	return "", errors.New("not found")
}

func (m *mockFileOps) ResolveWorkspaceData(_ string) (*sourcecontrol.WorkspaceTopology, error) {
	if m.resolveWsDataFunc != nil {
		return m.resolveWsDataFunc()
	}
	return nil, errors.New("not found")
}

func (m *mockFileOps) GitStatusPorcelain(context.Context, string) (sourcecontrol.GitFileStatusResult, error) {
	return sourcecontrol.GitFileStatusResult{Entries: map[string]string{}}, nil
}

func (m *mockFileOps) GitShowFileAtRev(context.Context, string, string, string, int64) (*sourcecontrol.GitFileContentAtRev, error) {
	return &sourcecontrol.GitFileContentAtRev{Content: []byte(""), Size: 0}, nil
}

func (m *mockFileOps) GitDiffFile(context.Context, string, string, string, string) (sourcecontrol.GitBoundedTextResult, error) {
	return sourcecontrol.GitBoundedTextResult{}, nil
}

func (m *mockFileOps) GitLogFile(context.Context, string, string, int) (sourcecontrol.GitBoundedTextResult, error) {
	return sourcecontrol.GitBoundedTextResult{}, nil
}

func (m *mockFileOps) GitBlamePorcelain(context.Context, string, string) (sourcecontrol.GitBoundedTextResult, error) {
	return sourcecontrol.GitBoundedTextResult{}, nil
}

func (m *mockFileOps) ResolveLoomDataDir() (string, error) {
	return "", errors.New("loom data directory not configured")
}

func (m *mockFileOps) GitCurrentBranch(context.Context, string) (string, error) {
	return "main", nil
}

func (m *mockFileOps) RepairCheckout(workspaceID, scope, target, repo string, force bool) (sourcecontrol.RepairResult, error) {
	if m.repairFunc != nil {
		return m.repairFunc(workspaceID, scope, target, repo, force)
	}
	return sourcecontrol.RepairResult{Repaired: false, Method: "none", Message: "not implemented"}, nil
}

func TestHandleFileCheckoutRepair_DisallowedRepoReturns400(t *testing.T) {
	fileOps := &mockFileOps{
		repairFunc: func(workspaceID, scope, target, repo string, force bool) (sourcecontrol.RepairResult, error) {
			if workspaceID != "ws-1" || scope != "agent" || target != "nova" || repo != "docs" || force {
				t.Fatalf("RepairCheckout args = (%q,%q,%q,%q,%v)", workspaceID, scope, target, repo, force)
			}
			return sourcecontrol.RepairResult{}, sourcecontrol.ErrAgentRepoNotAllowed
		},
	}
	svc := NewFileService(fileOps)
	body := strings.NewReader(`{"scope":"agent","target":"nova","repo":"docs"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-1/files/checkouts/repair", body)
	ctx := middleware.WithWorkspace(req.Context(), "ws-1")
	ctx = middleware.WithFileAccessGrant(ctx, testFileGrantIssuer.ReadWrite(true))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	HandleFileCheckoutRepair(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestIsBinaryContent(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		binary bool
	}{
		{"text", []byte("Hello, world!"), false},
		{"empty", []byte{}, false},
		{"binary", []byte{0x00, 0xFF, 0xFE}, true},
		{"utf8", []byte("日本語テキスト"), false},
		{"null byte", []byte{0x48, 0x65, 0x6C, 0x6C, 0x6F, 0x00}, true},
		{"invalid utf8 sequence", []byte{0x48, 0x65, 0x6C, 0x6C, 0x6F, 0xC0, 0xAF}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBinaryContent(tt.data); got != tt.binary {
				t.Errorf("IsBinaryContent(%v) = %v, want %v", tt.data, got, tt.binary)
			}
		})
	}
}
