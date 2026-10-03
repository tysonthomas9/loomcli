package publish

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

func ptr[T any](value T) *T { return &value }

func TestGitSettingsLeadMayChangeOnlyDeliveryMode(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	lead := review.Actor{Kind: "lead", ID: "L"}
	if _, err := SetGitSettings(ctx, fixture.store, "W", GitSettingsChange{DeliveryMode: ptr("trunk")}, lead, nil); err != nil {
		t.Fatalf("lead delivery mode: %v", err)
	}
	for name, change := range map[string]GitSettingsChange{
		"approve":        {LeadMayApprovePublish: ptr(false)},
		"merge":          {LeadMayMerge: ptr("when_green")},
		"mode and merge": {DeliveryMode: ptr("stack"), LeadMayMerge: ptr("when_green")},
	} {
		_, err := SetGitSettings(ctx, fixture.store, "W", change, lead, nil)
		var coded *loomgit.Error
		if !errors.As(err, &coded) || coded.Kind != loomgit.MergeNotAuthorized {
			t.Fatalf("lead %s = %v", name, err)
		}
	}
	if _, err := SetGitSettings(ctx, fixture.store, "W", GitSettingsChange{DeliveryMode: ptr("stack")},
		review.Actor{Kind: "agent", ID: "coder"}, nil); err == nil {
		t.Fatal("task agent changed delivery mode")
	}
	if _, err := SetGitSettings(ctx, fixture.store, "W", GitSettingsChange{LeadMayApprovePublish: ptr(false)},
		review.Actor{Kind: "human", ID: "tyson"}, []string{"LOOM_AGENT_NAME=lead"}); err == nil {
		t.Fatal("policy change carrying an agent marker was allowed")
	}
	settings, err := readGitSettings(ctx, fixture.store, "W")
	if err != nil || settings != (GitSettings{DeliveryMode: "trunk", LeadMayApprovePublish: true, LeadMayMerge: "off"}) {
		t.Fatalf("refused changes wrote settings: %+v, %v", settings, err)
	}
	warning, err := SetGitSettings(ctx, fixture.store, "W", GitSettingsChange{DeliveryMode: ptr("stack"),
		LeadMayApprovePublish: ptr(false), LeadMayMerge: ptr("when_green")}, review.Actor{Kind: "human", ID: "tyson"}, nil)
	if err != nil || warning == "" {
		t.Fatalf("human change = %q, %v", warning, err)
	}
	settings, err = readGitSettings(ctx, fixture.store, "W")
	if err != nil || settings != (GitSettings{DeliveryMode: "stack", LeadMayApprovePublish: false, LeadMayMerge: "when_green"}) {
		t.Fatalf("human settings = %+v, %v", settings, err)
	}
	if _, err := SetGitSettings(ctx, fixture.store, "W", GitSettingsChange{DeliveryMode: ptr("trunk"), LeadMayMerge: ptr("always")},
		review.Actor{Kind: "human", ID: "tyson"}, nil); err == nil {
		t.Fatal("invalid lead_may_merge accepted")
	}
	if mode, _ := fixture.store.DeliveryMode(ctx, "W"); mode != "stack" {
		t.Fatalf("invalid change wrote delivery mode %q", mode)
	}
}

func TestGitSettingsModeSwitchLeavesOpenPRsAndTargetsTrunkNext(t *testing.T) {
	useExplicitGitIdentity(t)
	fixture := newFixture(t)
	configureLocalWorkspace(t, fixture)
	ctx := context.Background()
	git(t, fixture.repo, "push", "-q", "origin", fixture.base+":refs/heads/develop")
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{
		Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base,
	}}); err != nil {
		t.Fatal(err)
	}
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	second := stackRevision(t, fixture, "B", 1, first.HeadSHA)
	for change, task := range map[string]string{"A": "task-A", "B": "task-B"} {
		if _, err := fixture.store.DriverChange(ctx, "W", task, "repo", change); err != nil {
			t.Fatal(err)
		}
	}
	forge := &fakeForge{}
	useLocalForge(t, forge)
	if _, err := PublishStackLocal(ctx, "W", LeadStackID("L"), "L", nil); err != nil {
		t.Fatal(err)
	}
	open := append([]stackpublish.PR(nil), forge.prs...)
	if len(open) != 2 || open[1].Base != open[0].Head {
		t.Fatalf("stacked PRs = %+v", open)
	}
	settings, _, err := SetGitSettingsLocal(ctx, "W", GitSettingsChange{DeliveryMode: ptr("trunk")}, review.Actor{Kind: "lead", ID: "L"}, nil)
	if err != nil || settings.DeliveryMode != "trunk" {
		t.Fatalf("switch to trunk = %+v, %v", settings, err)
	}
	if !reflect.DeepEqual(forge.prs, open) {
		t.Fatalf("mode switch changed open PRs: %+v, want %+v", forge.prs, open)
	}
	stackRevision(t, fixture, "C", 1, second.HeadSHA)
	if _, err := fixture.store.DriverChange(ctx, "W", "T", "repo", "C"); err != nil {
		t.Fatal(err)
	}
	result, err := PublishLocal(ctx, "W", "L", "C")
	if err != nil {
		t.Fatal(err)
	}
	if len(forge.prs) != 3 || forge.prs[2].Base != "develop" || !reflect.DeepEqual(forge.prs[:2], open) {
		t.Fatalf("next task after the switch = %+v; PRs = %+v", result, forge.prs)
	}
}
