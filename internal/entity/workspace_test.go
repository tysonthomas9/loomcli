package entity

import (
	"strings"
	"testing"
	"time"
)

func TestWorkspace_Validate(t *testing.T) {
	now := time.Now()
	validWorkspace := func() *Workspace {
		return &Workspace{
			ID:   "ws-001",
			Name: "my-workspace",
			Path: "/home/user/workspaces/my-workspace",
			Repos: []Repo{
				{Name: "backend", Path: "/home/user/repos/backend"},
			},
			CreatedAt: &now,
		}
	}

	t.Run("valid workspace with all fields passes", func(t *testing.T) {
		w := validWorkspace()
		w.Backend = "sqlite"
		if err := w.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid workspace with no repos passes", func(t *testing.T) {
		w := validWorkspace()
		w.Repos = []Repo{}
		if err := w.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid workspace with nil CreatedAt passes", func(t *testing.T) {
		w := validWorkspace()
		w.CreatedAt = nil
		if err := w.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty ID fails", func(t *testing.T) {
		w := validWorkspace()
		w.ID = ""
		err := w.Validate()
		if err == nil {
			t.Error("expected error for empty ID")
		} else if !strings.Contains(err.Error(), "id is required") {
			t.Errorf("error %q should contain %q", err.Error(), "id is required")
		}
	})

	t.Run("empty Name fails", func(t *testing.T) {
		w := validWorkspace()
		w.Name = ""
		err := w.Validate()
		if err == nil {
			t.Error("expected error for empty Name")
		} else if !strings.Contains(err.Error(), "name is required") {
			t.Errorf("error %q should contain %q", err.Error(), "name is required")
		}
	})

	t.Run("name exactly 64 chars passes", func(t *testing.T) {
		w := validWorkspace()
		w.Name = strings.Repeat("a", 64)
		if err := w.Validate(); err != nil {
			t.Errorf("unexpected error for 64-char name: %v", err)
		}
	})

	t.Run("name 65 chars fails", func(t *testing.T) {
		w := validWorkspace()
		w.Name = strings.Repeat("a", 65)
		err := w.Validate()
		if err == nil {
			t.Error("expected error for name > 64 chars")
		} else if !strings.Contains(err.Error(), "name exceeds maximum length") {
			t.Errorf("error %q should contain %q", err.Error(), "name exceeds maximum length")
		}
	})

	t.Run("name with space fails", func(t *testing.T) {
		w := validWorkspace()
		w.Name = "my workspace"
		err := w.Validate()
		if err == nil {
			t.Error("expected error for name with space")
		} else if !strings.Contains(err.Error(), "name must contain only") {
			t.Errorf("error %q should contain %q", err.Error(), "name must contain only")
		}
	})

	t.Run("name with dot fails", func(t *testing.T) {
		w := validWorkspace()
		w.Name = "my.workspace"
		err := w.Validate()
		if err == nil {
			t.Error("expected error for name with dot")
		} else if !strings.Contains(err.Error(), "name must contain only") {
			t.Errorf("error %q should contain %q", err.Error(), "name must contain only")
		}
	})

	t.Run("name with hyphens underscores digits passes", func(t *testing.T) {
		w := validWorkspace()
		w.Name = "my-workspace_01"
		if err := w.Validate(); err != nil {
			t.Errorf("unexpected error for name %q: %v", w.Name, err)
		}
	})

	t.Run("empty Path fails", func(t *testing.T) {
		w := validWorkspace()
		w.Path = ""
		err := w.Validate()
		if err == nil {
			t.Error("expected error for empty Path")
		} else if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("error %q should contain %q", err.Error(), "path is required")
		}
	})

	t.Run("repos with first repo invalid fails", func(t *testing.T) {
		w := validWorkspace()
		w.Repos = []Repo{
			{Name: "", Path: "/some/path"},
		}
		err := w.Validate()
		if err == nil {
			t.Error("expected error for invalid repo")
		} else if !strings.Contains(err.Error(), "repos[0]: name is required") {
			t.Errorf("error %q should contain %q", err.Error(), "repos[0]: name is required")
		}
	})

	t.Run("repos with second repo invalid fails", func(t *testing.T) {
		w := validWorkspace()
		w.Repos = []Repo{
			{Name: "valid-repo", Path: "/valid/path"},
			{Name: "", Path: "/some/path"},
		}
		err := w.Validate()
		if err == nil {
			t.Error("expected error for second invalid repo")
		} else if !strings.Contains(err.Error(), "repos[1]:") {
			t.Errorf("error %q should contain %q", err.Error(), "repos[1]:")
		}
	})
}

func TestRepo_Validate(t *testing.T) {
	validRepo := func() *Repo {
		return &Repo{
			Name:          "my-repo",
			Path:          "/home/user/repos/my-repo",
			DefaultBranch: "main",
			Remote:        "origin",
			Groups:        []string{"backend"},
			SourceRepoID:  "source-1",
		}
	}

	t.Run("valid repo with all fields passes", func(t *testing.T) {
		r := validRepo()
		if err := r.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid repo with minimal fields passes", func(t *testing.T) {
		r := &Repo{
			Name: "minimal-repo",
			Path: "/some/path",
		}
		if err := r.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty Name fails", func(t *testing.T) {
		r := validRepo()
		r.Name = ""
		err := r.Validate()
		if err == nil {
			t.Error("expected error for empty Name")
		} else if !strings.Contains(err.Error(), "name is required") {
			t.Errorf("error %q should contain %q", err.Error(), "name is required")
		}
	})

	t.Run("name 129 chars fails", func(t *testing.T) {
		r := validRepo()
		r.Name = strings.Repeat("a", 129)
		err := r.Validate()
		if err == nil {
			t.Error("expected error for name > 128 chars")
		} else if !strings.Contains(err.Error(), "name exceeds maximum length") {
			t.Errorf("error %q should contain %q", err.Error(), "name exceeds maximum length")
		}
	})

	t.Run("empty Path fails", func(t *testing.T) {
		r := validRepo()
		r.Path = ""
		err := r.Validate()
		if err == nil {
			t.Error("expected error for empty Path")
		} else if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("error %q should contain %q", err.Error(), "path is required")
		}
	})

	t.Run("remote 256 chars fails", func(t *testing.T) {
		r := validRepo()
		r.Remote = strings.Repeat("a", 256)
		err := r.Validate()
		if err == nil {
			t.Error("expected error for remote > 255 chars")
		} else if !strings.Contains(err.Error(), "remote exceeds maximum length") {
			t.Errorf("error %q should contain %q", err.Error(), "remote exceeds maximum length")
		}
	})

	t.Run("remote starting with hyphen fails", func(t *testing.T) {
		r := validRepo()
		r.Remote = "-upstream"
		err := r.Validate()
		if err == nil {
			t.Error("expected error for remote starting with '-'")
		} else if !strings.Contains(err.Error(), "remote must not start with") {
			t.Errorf("error %q should contain %q", err.Error(), "remote must not start with")
		}
	})

	t.Run("remote with slash fails", func(t *testing.T) {
		r := validRepo()
		r.Remote = "up/stream"
		err := r.Validate()
		if err == nil {
			t.Error("expected error for remote with '/'")
		} else if !strings.Contains(err.Error(), "remote contains invalid character") {
			t.Errorf("error %q should contain %q", err.Error(), "remote contains invalid character")
		}
	})

	t.Run("remote with spaces fails", func(t *testing.T) {
		r := validRepo()
		r.Remote = "my remote"
		err := r.Validate()
		if err == nil {
			t.Error("expected error for remote with spaces")
		} else if !strings.Contains(err.Error(), "remote contains invalid character") {
			t.Errorf("error %q should contain %q", err.Error(), "remote contains invalid character")
		}
	})

	t.Run("remote with alphanumeric hyphen underscore dot passes", func(t *testing.T) {
		r := validRepo()
		r.Remote = "my-remote_v2.1"
		if err := r.Validate(); err != nil {
			t.Errorf("unexpected error for remote %q: %v", r.Remote, err)
		}
	})

	t.Run("empty Remote passes", func(t *testing.T) {
		r := validRepo()
		r.Remote = ""
		if err := r.Validate(); err != nil {
			t.Errorf("unexpected error for empty remote: %v", err)
		}
	})
}

func TestRepo_EffectiveRemote(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		want   string
	}{
		{"explicit remote", "upstream", "upstream"},
		{"empty remote defaults to origin", "", "origin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Repo{Remote: tt.remote}
			if got := r.EffectiveRemote(); got != tt.want {
				t.Errorf("Repo{Remote: %q}.EffectiveRemote() = %q, want %q", tt.remote, got, tt.want)
			}
		})
	}
}

func TestRepo_EffectiveDefaultBranch(t *testing.T) {
	tests := []struct {
		name          string
		defaultBranch string
		want          string
	}{
		{"explicit branch", "develop", "develop"},
		{"empty branch defaults to main", "", "main"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Repo{DefaultBranch: tt.defaultBranch}
			if got := r.EffectiveDefaultBranch(); got != tt.want {
				t.Errorf("Repo{DefaultBranch: %q}.EffectiveDefaultBranch() = %q, want %q", tt.defaultBranch, got, tt.want)
			}
		})
	}
}

func TestRepo_EffectiveSourceRepoID(t *testing.T) {
	tests := []struct {
		name         string
		sourceRepoID string
		repoName     string
		want         string
	}{
		{"explicit source repo ID", "custom-id", "my-repo", "custom-id"},
		{"empty source repo ID defaults to name", "", "my-repo", "my-repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Repo{SourceRepoID: tt.sourceRepoID, Name: tt.repoName}
			if got := r.EffectiveSourceRepoID(); got != tt.want {
				t.Errorf("Repo{SourceRepoID: %q, Name: %q}.EffectiveSourceRepoID() = %q, want %q",
					tt.sourceRepoID, tt.repoName, got, tt.want)
			}
		})
	}
}

func TestWorkspace_RepoByName(t *testing.T) {
	t.Run("finds existing repo", func(t *testing.T) {
		w := &Workspace{
			Repos: []Repo{
				{Name: "frontend", Path: "/repos/frontend"},
				{Name: "backend", Path: "/repos/backend"},
			},
		}
		got := w.RepoByName("backend")
		if got == nil {
			t.Fatal("expected non-nil result for existing repo")
		}
		if got.Name != "backend" {
			t.Errorf("got Name = %q, want %q", got.Name, "backend")
		}
		if got.Path != "/repos/backend" {
			t.Errorf("got Path = %q, want %q", got.Path, "/repos/backend")
		}
	})

	t.Run("returns nil for non-existent repo", func(t *testing.T) {
		w := &Workspace{
			Repos: []Repo{
				{Name: "frontend", Path: "/repos/frontend"},
			},
		}
		got := w.RepoByName("missing")
		if got != nil {
			t.Errorf("expected nil for non-existent repo, got %+v", got)
		}
	})

	t.Run("returns nil for workspace with no repos", func(t *testing.T) {
		w := &Workspace{}
		got := w.RepoByName("anything")
		if got != nil {
			t.Errorf("expected nil for workspace with no repos, got %+v", got)
		}
	})

	t.Run("returns first match for duplicate repo names", func(t *testing.T) {
		w := &Workspace{
			Repos: []Repo{
				{Name: "shared", Path: "/repos/shared-first"},
				{Name: "shared", Path: "/repos/shared-second"},
			},
		}
		got := w.RepoByName("shared")
		if got == nil {
			t.Fatal("expected non-nil result for duplicate repo name")
		}
		if got.Path != "/repos/shared-first" {
			t.Errorf("got Path = %q, want %q (first match)", got.Path, "/repos/shared-first")
		}
	})
}

func TestWorkspace_GroupNames(t *testing.T) {
	t.Run("deduplicates and sorts groups across repos", func(t *testing.T) {
		w := &Workspace{
			Repos: []Repo{
				{Name: "repo1", Path: "/r1", Groups: []string{"backend", "infra"}},
				{Name: "repo2", Path: "/r2", Groups: []string{"backend", "api"}},
			},
		}
		got := w.GroupNames()
		want := []string{"api", "backend", "infra"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("repos with no groups returns empty slice", func(t *testing.T) {
		w := &Workspace{
			Repos: []Repo{
				{Name: "repo1", Path: "/r1"},
				{Name: "repo2", Path: "/r2"},
			},
		}
		got := w.GroupNames()
		if got == nil {
			t.Error("expected non-nil empty slice, got nil")
		}
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %v", got)
		}
	})

	t.Run("workspace with no repos returns empty slice", func(t *testing.T) {
		w := &Workspace{}
		got := w.GroupNames()
		if got == nil {
			t.Error("expected non-nil empty slice, got nil")
		}
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %v", got)
		}
	})
}
