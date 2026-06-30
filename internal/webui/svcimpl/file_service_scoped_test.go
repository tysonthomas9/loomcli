package svcimpl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// scopedMockFileOps is a minimal ops.FileOps that only resolves the workspace
// root, so these tests exercise the real fileServiceImpl scoped code paths.
type scopedMockFileOps struct {
	wsRoot string
	wsErr  error
}

func (m scopedMockFileOps) ResolveAgentWorktree(_, _ string) (*ops.AgentWorktree, error) {
	return nil, errors.New("not used")
}
func (m scopedMockFileOps) ResolveAgentWorktreeOrPrimary(_, _ string) (*ops.AgentWorktree, error) {
	return nil, errors.New("not used")
}
func (m scopedMockFileOps) ResolveWorkspaceRoot(_ string) (string, error) {
	if m.wsErr != nil {
		return "", m.wsErr
	}
	return m.wsRoot, nil
}

func scopedSvc(root string) service.FileService {
	return NewFileService(scopedMockFileOps{wsRoot: root})
}

func wantKind(t *testing.T, err error, kind service.ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error of kind %q, got nil", kind)
	}
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("error = %T %v, want *service.ServiceError", err, err)
	}
	if svcErr.Kind != kind {
		t.Fatalf("error kind = %q, want %q", svcErr.Kind, kind)
	}
}

func TestFileServiceImpl_ListDirectoryScoped_HidesGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package m"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	res, err := scopedSvc(dir).ListDirectoryScoped(context.Background(), "ws", service.ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("ListDirectoryScoped: %v", err)
	}
	var sawMain, sawGit bool
	for _, e := range res.Entries {
		sawMain = sawMain || e.Name == "main.go"
		sawGit = sawGit || e.Name == ".git"
	}
	if !sawMain {
		t.Errorf("main.go missing from listing: %+v", res.Entries)
	}
	if sawGit {
		t.Errorf(".git must be hidden from listing: %+v", res.Entries)
	}
}

func TestFileServiceImpl_ReadFileScoped_ReadsContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("body"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := scopedSvc(dir).ReadFileScoped(context.Background(), "ws", service.ScopeWorkspace, "", "f.txt")
	if err != nil {
		t.Fatalf("ReadFileScoped: %v", err)
	}
	if res.Content != "body" {
		t.Errorf("content = %q, want %q", res.Content, "body")
	}
}

func TestFileServiceImpl_ReadFileScoped_GitDenied(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := scopedSvc(dir).ReadFileScoped(context.Background(), "ws", service.ScopeWorkspace, "", ".git/config")
	wantKind(t, err, service.KindForbidden)
}

func TestFileServiceImpl_Scoped_UnsupportedScope(t *testing.T) {
	_, err := scopedSvc(t.TempDir()).ListDirectoryScoped(context.Background(), "ws", service.FileScope("bogus"), "", "")
	wantKind(t, err, service.KindValidation)
}

func TestFileServiceImpl_Scoped_WorkspaceRejectsTarget(t *testing.T) {
	_, err := scopedSvc(t.TempDir()).ListDirectoryScoped(context.Background(), "ws", service.ScopeWorkspace, "some-repo", "")
	wantKind(t, err, service.KindValidation)
}

func TestFileServiceImpl_Scoped_WorkspaceNotCheckedOut(t *testing.T) {
	svc := NewFileService(scopedMockFileOps{wsErr: errors.New("workspace not checked out on this machine")})
	_, err := svc.ListDirectoryScoped(context.Background(), "ws", service.ScopeWorkspace, "", "")
	wantKind(t, err, service.KindNotFound)
}
