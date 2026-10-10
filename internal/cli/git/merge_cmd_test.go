package git

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
)

// stubMergeQueue runs loom merge in env against task-1 (change-2 in web,
// change-1 in api) and records every queued merge.
func stubMergeQueue(t *testing.T, env map[string]string, refusal error) *[]string {
	t.Helper()
	stubVerdictStore(t, env)
	oldResolver, oldQueue := mergeResolver, queueMergeLocal
	t.Cleanup(func() { mergeResolver, queueMergeLocal = oldResolver, oldQueue })
	mergeResolver = func() (*cli.Resolver, error) {
		return &cli.Resolver{Workspace: "workspace", Config: &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
			"workspace": {ID: "workspace-1"},
		}}}, nil
	}
	var calls []string
	queueMergeLocal = func(_ context.Context, workspace, change string, actor publish.MergeActor) (publish.MergeStackView, error) {
		if refusal != nil {
			return publish.MergeStackView{}, refusal
		}
		calls = append(calls, workspace+" "+change+" "+actor.Kind+":"+actor.ID)
		return publish.MergeStackView{StackID: "feature", Target: change, Phase: "ready",
			Layers: []publish.MergeLayerView{{Change: change, State: "ready", PRURL: "https://example/pr/1"}}}, nil
	}
	return &calls
}

func runMergeCommand(t *testing.T, task string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := *mergeCmd
	cmd.SetOut(&out)
	err := cmd.RunE(&cmd, []string{task})
	return out.String(), err
}

func TestLeadMergeQueuesEachRepoOfTheTask(t *testing.T) {
	calls := stubMergeQueue(t, leadEnv(), nil)
	out, err := runMergeCommand(t, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(*calls, "|") != "workspace-1 change-2 lead:lead-1|workspace-1 change-1 lead:lead-1" {
		t.Fatalf("queued %v", *calls)
	}
	if !strings.Contains(out, "merge queued: stack feature up to change-2 (ready)") {
		t.Fatalf("output %q", out)
	}
}

func TestLeadMergeTakesAChange(t *testing.T) {
	calls := stubMergeQueue(t, leadEnv(), nil)
	if _, err := runMergeCommand(t, "change-9"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*calls, "|") != "workspace-1 change-9 lead:lead-1" {
		t.Fatalf("queued %v", *calls)
	}
}

func TestMergeIsOnlyTheLeadsCommand(t *testing.T) {
	for name, env := range map[string]map[string]string{"human": humanEnv(), "task agent": taskAgentEnv()} {
		t.Run(name, func(t *testing.T) {
			calls := stubMergeQueue(t, env, nil)
			_, err := runMergeCommand(t, "task-1")
			if err == nil || !strings.Contains(err.Error(), "lead's command") || len(*calls) != 0 {
				t.Fatalf("err=%v queued %v", err, *calls)
			}
		})
	}
}

func TestLeadMergeShowsLeadMayMergeOff(t *testing.T) {
	stubMergeQueue(t, leadEnv(), loomgit.NewError(loomgit.MergeNotAuthorized, publish.LeadMayMergeOff, nil))
	_, err := runMergeCommand(t, "task-1")
	if err == nil || !strings.Contains(err.Error(), "Lead may merge is off") {
		t.Fatalf("err=%v", err)
	}
}
