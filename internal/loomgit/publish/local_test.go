package publish

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestPublishRecordedRequiresVerdictBeforePush(t *testing.T) {
	fixture := newFixture(t)
	revision := fixture.revision(t, 1, fixture.base, "change", "source")
	ctx := context.Background()
	if _, err := fixture.store.DriverChange(ctx, "W", "T", "repo", "C"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{
		Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base,
	}}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}},
	}}
	forge := &fakeForge{}
	request := func() (Result, error) {
		return publishRecorded(ctx, fixture.store, cfg, "W", "L", "C", forge, "fixture-token", "owner/repo")
	}
	_, err := request()
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != loomgit.ReviewRequired {
		t.Fatalf("unapproved publish error = %v", err)
	}
	if forge.creates != 0 {
		t.Fatal("unapproved change opened a PR")
	}
	fixture.approve(t, revision)
	result, err := request()
	if err != nil || result.Revision.HeadSHA != revision.HeadSHA || result.PRURL == "" || result.AlreadyExists {
		t.Fatalf("approved publish = %+v, %v", result, err)
	}
	result, err = request()
	if err != nil || !result.AlreadyExists || forge.creates != 1 {
		t.Fatalf("repeated publish = %+v, %v; creates=%d", result, err, forge.creates)
	}
}
