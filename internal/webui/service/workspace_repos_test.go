package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// makeFakeRepo creates a directory containing a .git entry so it passes
// AddWorkspaceRepo's validation.
func makeFakeRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("create fake repo: %v", err)
	}
	return dir
}

func TestAddWorkspaceRepo_Success(t *testing.T) {
	path := writeConfig(t, `version: 1
workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos:
      - name: existing
        path: /tmp/alpha/existing
        default_branch: main
`)
	repo := makeFakeRepo(t, "newrepo")

	svc := &workspaceServiceImpl{}
	_, err := svc.AddWorkspaceRepo(context.Background(), "ws-1", AddRepoParams{
		Path:          repo,
		DefaultBranch: "develop",
		Remote:        "upstream",
		Groups:        []string{"backend"},
	})
	if err != nil {
		t.Fatalf("AddWorkspaceRepo: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var got loomConfigForRepoBranch
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	ws := got.Workspaces["alpha"]
	if len(ws.Repos) != 2 {
		t.Fatalf("repos count: got %d, want 2", len(ws.Repos))
	}
	added := ws.Repos[1]
	if added.Name != "newrepo" {
		t.Errorf("name = %q, want newrepo", added.Name)
	}
	if added.Path != repo {
		t.Errorf("path = %q, want %q", added.Path, repo)
	}
	if added.DefaultBranch != "develop" {
		t.Errorf("default_branch = %q", added.DefaultBranch)
	}
	if added.Remote != "upstream" {
		t.Errorf("remote = %q", added.Remote)
	}
	if added.SourceRepoID != "newrepo" {
		t.Errorf("source_repo_id = %q, want newrepo", added.SourceRepoID)
	}
	if len(added.Groups) != 1 || added.Groups[0] != "backend" {
		t.Errorf("groups = %v", added.Groups)
	}
	// Sibling preserved.
	if ws.Repos[0].Name != "existing" {
		t.Errorf("existing repo lost: %v", ws.Repos[0])
	}
}

func TestAddWorkspaceRepo_EmptyPath(t *testing.T) {
	writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos: []
`)
	svc := &workspaceServiceImpl{}
	_, err := svc.AddWorkspaceRepo(context.Background(), "ws-1", AddRepoParams{Path: ""})
	if se, ok := err.(*ServiceError); !ok || se.Kind != KindValidation {
		t.Errorf("want validation error, got %#v", err)
	}
}

func TestAddWorkspaceRepo_PathDoesNotExist(t *testing.T) {
	writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos: []
`)
	svc := &workspaceServiceImpl{}
	_, err := svc.AddWorkspaceRepo(context.Background(), "ws-1", AddRepoParams{Path: "/does/not/exist/anywhere"})
	if se, ok := err.(*ServiceError); !ok || se.Kind != KindValidation {
		t.Errorf("want validation error, got %#v", err)
	}
}

func TestAddWorkspaceRepo_NotAGitRepo(t *testing.T) {
	writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos: []
`)
	dir := t.TempDir() // no .git inside
	svc := &workspaceServiceImpl{}
	_, err := svc.AddWorkspaceRepo(context.Background(), "ws-1", AddRepoParams{Path: dir})
	if se, ok := err.(*ServiceError); !ok || se.Kind != KindValidation {
		t.Errorf("want validation error, got %#v", err)
	}
}

func TestAddWorkspaceRepo_DuplicateName(t *testing.T) {
	writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos:
      - name: dupe
        path: /tmp/alpha/dupe
`)
	repo := makeFakeRepo(t, "dupe")
	svc := &workspaceServiceImpl{}
	_, err := svc.AddWorkspaceRepo(context.Background(), "ws-1", AddRepoParams{Path: repo})
	if se, ok := err.(*ServiceError); !ok || se.Kind != KindConflict {
		t.Errorf("want conflict error, got %#v", err)
	}
}

func TestAddWorkspaceRepo_NameOverride(t *testing.T) {
	path := writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos: []
`)
	repo := makeFakeRepo(t, "unwantedname")
	svc := &workspaceServiceImpl{}
	_, err := svc.AddWorkspaceRepo(context.Background(), "ws-1", AddRepoParams{Path: repo, Name: "custom"})
	if err != nil {
		t.Fatalf("AddWorkspaceRepo: %v", err)
	}
	raw, _ := os.ReadFile(path)
	var got loomConfigForRepoBranch
	_ = yaml.Unmarshal(raw, &got)
	if got.Workspaces["alpha"].Repos[0].Name != "custom" {
		t.Errorf("name not overridden: %v", got.Workspaces["alpha"].Repos[0])
	}
}

func TestAddWorkspaceRepo_InvalidName(t *testing.T) {
	writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos: []
`)
	repo := makeFakeRepo(t, "ok")
	svc := &workspaceServiceImpl{}
	_, err := svc.AddWorkspaceRepo(context.Background(), "ws-1", AddRepoParams{Path: repo, Name: "bad name with spaces!"})
	if se, ok := err.(*ServiceError); !ok || se.Kind != KindValidation {
		t.Errorf("want validation error, got %#v", err)
	}
}

func TestAddWorkspaceRepo_UnknownWorkspace(t *testing.T) {
	writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos: []
`)
	repo := makeFakeRepo(t, "x")
	svc := &workspaceServiceImpl{}
	_, err := svc.AddWorkspaceRepo(context.Background(), "ws-unknown", AddRepoParams{Path: repo})
	if se, ok := err.(*ServiceError); !ok || se.Kind != KindNotFound {
		t.Errorf("want not-found, got %#v", err)
	}
}

func TestRemoveWorkspaceRepo_Success(t *testing.T) {
	path := writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos:
      - name: a
        path: /tmp/a
      - name: b
        path: /tmp/b
`)
	svc := &workspaceServiceImpl{}
	_, err := svc.RemoveWorkspaceRepo(context.Background(), "ws-1", "a")
	if err != nil {
		t.Fatalf("RemoveWorkspaceRepo: %v", err)
	}
	raw, _ := os.ReadFile(path)
	var got loomConfigForRepoBranch
	_ = yaml.Unmarshal(raw, &got)
	repos := got.Workspaces["alpha"].Repos
	if len(repos) != 1 || repos[0].Name != "b" {
		t.Errorf("after remove: %+v", repos)
	}
}

func TestRemoveWorkspaceRepo_LastRepoAllowed(t *testing.T) {
	path := writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos:
      - name: only
        path: /tmp/only
`)
	svc := &workspaceServiceImpl{}
	_, err := svc.RemoveWorkspaceRepo(context.Background(), "ws-1", "only")
	if err != nil {
		t.Fatalf("RemoveWorkspaceRepo: %v", err)
	}
	raw, _ := os.ReadFile(path)
	var got loomConfigForRepoBranch
	_ = yaml.Unmarshal(raw, &got)
	if len(got.Workspaces["alpha"].Repos) != 0 {
		t.Errorf("workspace should have zero repos, got %d", len(got.Workspaces["alpha"].Repos))
	}
}

func TestRemoveWorkspaceRepo_UnknownWorkspace(t *testing.T) {
	writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos:
      - name: a
        path: /tmp/a
`)
	svc := &workspaceServiceImpl{}
	_, err := svc.RemoveWorkspaceRepo(context.Background(), "nope", "a")
	if se, ok := err.(*ServiceError); !ok || se.Kind != KindNotFound {
		t.Errorf("want not-found, got %#v", err)
	}
}

func TestRemoveWorkspaceRepo_UnknownRepo(t *testing.T) {
	writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos:
      - name: a
        path: /tmp/a
`)
	svc := &workspaceServiceImpl{}
	_, err := svc.RemoveWorkspaceRepo(context.Background(), "ws-1", "missing")
	if se, ok := err.(*ServiceError); !ok || se.Kind != KindNotFound {
		t.Errorf("want not-found, got %#v", err)
	}
}

func TestRemoveWorkspaceRepo_EmptyName(t *testing.T) {
	writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos: []
`)
	svc := &workspaceServiceImpl{}
	_, err := svc.RemoveWorkspaceRepo(context.Background(), "ws-1", "")
	if se, ok := err.(*ServiceError); !ok || se.Kind != KindValidation {
		t.Errorf("want validation, got %#v", err)
	}
}

func TestRemoveWorkspaceRepo_RejectsPathTraversal(t *testing.T) {
	writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos:
      - name: a
        path: /tmp/a
`)
	svc := &workspaceServiceImpl{}
	for _, badName := range []string{"../etc/passwd", "foo/bar", "with space", "with;semi"} {
		t.Run(badName, func(t *testing.T) {
			_, err := svc.RemoveWorkspaceRepo(context.Background(), "ws-1", badName)
			if se, ok := err.(*ServiceError); !ok || se.Kind != KindValidation {
				t.Errorf("want validation for %q, got %#v", badName, err)
			}
		})
	}
}
