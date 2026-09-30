package apply

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestApplyLocalSelectsRequestedLeadCheckout(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	secondLead := filepath.Join(t.TempDir(), "lead-m")
	fixture.git(t, "worktree", "add", "-q", "-b", "loom/ws/W/interactive/M", secondLead, fixture.base)
	if _, err := fixture.store.DriverChange(ctx, "W", "T1", "repo", "C1"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{
		{Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.dir, Branch: "loom/ws/W/interactive/L", BaseSHA: fixture.base, Mode: "worktree"},
		{Workspace: "W", Lead: "M", Repo: "repo", Path: secondLead, Branch: "loom/ws/W/interactive/M", BaseSHA: fixture.base, Mode: "worktree"},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"W": {ID: "W", Repos: []config.RepoConfig{{Name: "repo", Path: fixture.dir}}},
	}}
	request := Request{Workspace: "W", Lead: "M", Change: "C1", Revision: 1, RequestID: "apply-m"}
	result, err := applyLocalWithStore(ctx, request, fixture.store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.HeadSHA != fixture.source {
		t.Fatalf("applied head = %s, want %s", result.HeadSHA, fixture.source)
	}
	leadRunner, err := gitexec.New(secondLead, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Test", Email: "test@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	leadHead, err := leadRunner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(leadHead)); got != fixture.source {
		t.Fatalf("selected lead head = %s, want %s", got, fixture.source)
	}
	if got := fixture.git(t, "rev-parse", "HEAD"); got != fixture.base {
		t.Fatalf("other lead head changed to %s", got)
	}
	request.Lead = "missing"
	_, err = applyLocalWithStore(ctx, request, fixture.store, cfg)
	var loomError *loomgit.Error
	if !errors.As(err, &loomError) || !strings.Contains(err.Error(), "working area") {
		t.Fatalf("missing lead error = %v", err)
	}
}

func TestWorkingAreaForRepoFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name  string
		areas []journal.WorkingArea
	}{
		{name: "missing"},
		{name: "empty path", areas: []journal.WorkingArea{{Repo: "repo"}}},
		{name: "ambiguous", areas: []journal.WorkingArea{{Repo: "repo", Path: "/first"}, {Repo: "repo", Path: "/second"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := workingAreaForRepo(test.areas, "repo"); err == nil {
				t.Fatal("expected working area selection error")
			}
		})
	}
}
