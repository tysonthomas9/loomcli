package svcimpl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// scopedMockFileOps is a minimal ops.FileOps that only resolves the workspace
// root, so these tests exercise the real fileServiceImpl scoped code paths.
type scopedMockFileOps struct {
	wsRoot    string
	repoRoot  string
	agentRoot string
	wsErr     error
}

func (m scopedMockFileOps) ResolveAgentWorktree(_, name string) (*ops.AgentWorktree, error) {
	if name != "agent-a" || m.agentRoot == "" {
		return nil, errors.New("agent worktree not found")
	}
	return &ops.AgentWorktree{Name: name, Path: m.agentRoot, RepoName: "repo-a"}, nil
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
func (m scopedMockFileOps) ResolveWorkspaceData(_ string) (*ops.WorkspaceData, error) {
	if m.wsErr != nil {
		return nil, m.wsErr
	}
	ws := &ops.WorkspaceData{
		ID:   "ws",
		Path: m.wsRoot,
		Repos: []ops.WorkspaceRepo{{
			Name: "repo-a",
			Path: m.repoRoot,
		}},
		Agents: []ops.WorkspaceAgentInfo{{Name: "agent-a"}},
	}
	return ws, nil
}

func scopedSvc(root string) service.FileService {
	return NewFileService(scopedMockFileOps{wsRoot: root})
}

type scopedCase struct {
	name   string
	scope  service.FileScope
	target string
	root   string
}

func setupScopedService(t *testing.T) (service.FileService, []scopedCase) {
	t.Helper()
	wsRoot := t.TempDir()
	repoRoot := filepath.Join(wsRoot, "repo-a")
	agentRoot := filepath.Join(wsRoot, "worktrees", "repo-a", "agent-a")
	for _, dir := range []string{repoRoot, agentRoot} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewFileService(scopedMockFileOps{
		wsRoot:    wsRoot,
		repoRoot:  repoRoot,
		agentRoot: agentRoot,
	})
	return svc, []scopedCase{
		{name: "workspace", scope: service.ScopeWorkspace, root: wsRoot},
		{name: "repo", scope: service.ScopeRepo, target: "repo-a", root: repoRoot},
		{name: "agent", scope: service.ScopeAgent, target: "agent-a", root: agentRoot},
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
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

func TestFileServiceImpl_ReadFileScoped_GitExplicitReadAllowed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := scopedSvc(dir).ReadFileScoped(context.Background(), "ws", service.ScopeWorkspace, "", ".git/config")
	if err != nil {
		t.Fatalf("ReadFileScoped .git/config: %v", err)
	}
	if res.Content != "x" {
		t.Fatalf("content = %q, want x", res.Content)
	}
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

func TestFileServiceImpl_PhaseA_CRUDAndVisibilityAllScopes(t *testing.T) {
	svc, scopes := setupScopedService(t)
	ctx := context.Background()

	for _, sc := range scopes {
		t.Run(sc.name, func(t *testing.T) {
			mustWrite(t, filepath.Join(sc.root, ".env"), "OLD=1")
			mustWrite(t, filepath.Join(sc.root, ".git", "config"), "git config")
			if err := os.MkdirAll(filepath.Join(sc.root, "node_modules", "pkg"), 0755); err != nil {
				t.Fatal(err)
			}

			list, err := svc.ListDirectoryScoped(ctx, "ws", sc.scope, sc.target, "")
			if err != nil {
				t.Fatalf("ListDirectoryScoped: %v", err)
			}
			names := map[string]bool{}
			for _, entry := range list.Entries {
				names[entry.Name] = true
			}
			if names[".git"] {
				t.Fatalf(".git listed in %s scope: %+v", sc.name, list.Entries)
			}
			if !names["node_modules"] {
				t.Fatalf("node_modules missing in %s scope: %+v", sc.name, list.Entries)
			}

			readEnv, err := svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, ".env")
			if err != nil {
				t.Fatalf("read .env: %v", err)
			}
			if readEnv.Content != "OLD=1" {
				t.Fatalf(".env content = %q", readEnv.Content)
			}
			if err := svc.WriteFileScoped(ctx, "ws", sc.scope, sc.target, ".env", "NEW=1"); err != nil {
				t.Fatalf("write .env: %v", err)
			}
			readEnv, err = svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, ".env")
			if err != nil {
				t.Fatalf("read written .env: %v", err)
			}
			if readEnv.Content != "NEW=1" {
				t.Fatalf("written .env content = %q", readEnv.Content)
			}

			if err := svc.WriteFileScoped(ctx, "ws", sc.scope, sc.target, ".git/config", "mutated"); err != nil {
				t.Fatalf("write .git/config: %v", err)
			}
			readGit, err := svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, ".git/config")
			if err != nil {
				t.Fatalf("read .git/config: %v", err)
			}
			if readGit.Content != "mutated" {
				t.Fatalf(".git/config content = %q", readGit.Content)
			}
			if err := svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, ".git/refs/heads"); err != nil {
				t.Fatalf("mkdir .git path: %v", err)
			}
			if err := svc.MovePathScoped(ctx, "ws", sc.scope, sc.target, ".git/config", ".git/config.moved", false); err != nil {
				t.Fatalf("move .git path: %v", err)
			}
			if err := svc.DeletePathScoped(ctx, "ws", sc.scope, sc.target, ".git/config.moved", false); err != nil {
				t.Fatalf("delete .git path: %v", err)
			}

			mustWrite(t, filepath.Join(sc.root, "nonempty", "file.txt"), "x")
			err = svc.DeletePathScoped(ctx, "ws", sc.scope, sc.target, "nonempty", false)
			wantKind(t, err, service.KindConflict)
			if err := svc.DeletePathScoped(ctx, "ws", sc.scope, sc.target, "nonempty", true); err != nil {
				t.Fatalf("recursive delete: %v", err)
			}
			if _, err := os.Stat(filepath.Join(sc.root, "nonempty")); !os.IsNotExist(err) {
				t.Fatalf("nonempty dir still exists after recursive delete: %v", err)
			}

			mustWrite(t, filepath.Join(sc.root, "file-exists"), "x")
			err = svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, "file-exists")
			wantKind(t, err, service.KindConflict)
			if err := svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, "new/dir"); err != nil {
				t.Fatalf("mkdir nested: %v", err)
			}

			mustWrite(t, filepath.Join(sc.root, "move-src.txt"), "src")
			mustWrite(t, filepath.Join(sc.root, "move-dst.txt"), "dst")
			err = svc.MovePathScoped(ctx, "ws", sc.scope, sc.target, "move-src.txt", "move-dst.txt", false)
			wantKind(t, err, service.KindConflict)
			if err := svc.MovePathScoped(ctx, "ws", sc.scope, sc.target, "move-src.txt", "move-dst.txt", true); err != nil {
				t.Fatalf("move overwrite: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(sc.root, "move-dst.txt"))
			if err != nil {
				t.Fatalf("read overwritten dest: %v", err)
			}
			if string(got) != "src" {
				t.Fatalf("move dest content = %q, want src", string(got))
			}

			large := strings.Repeat("A", maxRequestBody+25)
			mustWrite(t, filepath.Join(sc.root, "large.txt"), large)
			readLarge, err := svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, "large.txt")
			if err != nil {
				t.Fatalf("read large: %v", err)
			}
			if !readLarge.Truncated {
				t.Fatalf("large read truncated = false")
			}
			if len(readLarge.Content) != maxRequestBody {
				t.Fatalf("large content len = %d, want %d", len(readLarge.Content), maxRequestBody)
			}
		})
	}
}

func TestFileServiceImpl_PhaseA_StructuralGuardsAllScopes(t *testing.T) {
	svc, scopes := setupScopedService(t)
	ctx := context.Background()

	for _, sc := range scopes {
		t.Run(sc.name, func(t *testing.T) {
			mustWrite(t, filepath.Join(sc.root, "existing.txt"), "x")
			mustWrite(t, filepath.Join(sc.root, "target.txt"), "target")
			mustWrite(t, filepath.Join(filepath.Dir(sc.root), "outside.txt"), "outside")
			if err := os.Symlink(filepath.Join(sc.root, "existing.txt"), filepath.Join(sc.root, "link.txt")); err != nil {
				t.Skip("cannot create symlinks on this platform")
			}

			_, err := svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, "../outside.txt")
			wantKind(t, err, service.KindForbidden)
			err = svc.WriteFileScoped(ctx, "ws", sc.scope, sc.target, "../outside.txt", "bad")
			wantKind(t, err, service.KindForbidden)
			err = svc.DeletePathScoped(ctx, "ws", sc.scope, sc.target, "../outside.txt", false)
			wantKind(t, err, service.KindForbidden)
			err = svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, "../outside-dir")
			wantKind(t, err, service.KindForbidden)
			err = svc.MovePathScoped(ctx, "ws", sc.scope, sc.target, "existing.txt", "../outside-move.txt", false)
			wantKind(t, err, service.KindForbidden)

			_, err = svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, "link.txt")
			wantKind(t, err, service.KindForbidden)
			err = svc.WriteFileScoped(ctx, "ws", sc.scope, sc.target, "link.txt", "bad")
			wantKind(t, err, service.KindForbidden)
			err = svc.DeletePathScoped(ctx, "ws", sc.scope, sc.target, "link.txt", false)
			wantKind(t, err, service.KindForbidden)
			err = svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, "link.txt")
			wantKind(t, err, service.KindForbidden)
			err = svc.MovePathScoped(ctx, "ws", sc.scope, sc.target, "link.txt", "moved-link.txt", false)
			wantKind(t, err, service.KindForbidden)
		})
	}
}

func TestFileServiceImpl_PhaseA_InvalidTargetsRejected(t *testing.T) {
	svc, _ := setupScopedService(t)
	ctx := context.Background()

	_, err := svc.ListDirectoryScoped(ctx, "ws", service.ScopeRepo, "missing-repo", "")
	wantKind(t, err, service.KindNotFound)
	_, err = svc.ListDirectoryScoped(ctx, "ws", service.ScopeAgent, "missing-agent", "")
	wantKind(t, err, service.KindNotFound)
}
