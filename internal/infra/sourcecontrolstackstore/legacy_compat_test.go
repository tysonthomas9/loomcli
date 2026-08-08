package stackstore_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	stackstore "github.com/tysonthomas9/loomcli/internal/infra/sourcecontrolstackstore"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

const legacyStacksJSON = `{
  "version": 1,
  "workspaces": {
    "LEGACY": {
      "stacks": {
        "epic:LEGACY-1": {
          "stack": {
            "id": "epic:LEGACY-1",
            "workspaceKey": "LEGACY",
            "repoName": "acme/legacy",
            "rootBase": "release/legacy",
            "defaultCommitMode": "agent_commit",
            "createdAt": "2026-06-01T10:00:00Z",
            "updatedAt": "2026-06-02T11:00:00Z"
          },
          "nodes": {
            "LEGACY-1": {
              "stackId": "epic:LEGACY-1",
              "taskId": "LEGACY-1",
              "outputBranch": "loom/stack/epic-LEGACY-1/LEGACY-1",
              "commitMode": "agent_commit",
              "state": "published",
              "prNumber": 17,
              "prUrl": "https://example.test/acme/legacy/pull/17",
              "outputSha": "1111111111111111111111111111111111111111",
              "lastPublishedAt": "2026-06-02T10:30:00Z",
              "createdAt": "2026-06-01T10:05:00Z",
              "updatedAt": "2026-06-02T10:30:00Z"
            },
            "LEGACY-2": {
              "stackId": "epic:LEGACY-1",
              "taskId": "LEGACY-2",
              "baseTaskId": "LEGACY-1",
              "outputBranch": "loom/stack/epic-LEGACY-1/LEGACY-2",
              "commitMode": "squash_on_publish",
              "state": "conflicted",
              "prNumber": 18,
              "prUrl": "https://example.test/acme/legacy/pull/18",
              "outputSha": "2222222222222222222222222222222222222222",
              "lastPublishedAt": "2026-06-02T11:00:00Z",
              "createdAt": "2026-06-01T10:10:00Z",
              "updatedAt": "2026-06-02T11:00:00Z"
            }
          }
        }
      }
    }
  }
}`

func TestLegacyStacksJSONLoadsAndRoundTripsThroughCanonicalModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.json"), []byte(legacyStacksJSON), 0o600))

	store := stackstore.New(dir)
	lifecycle, err := sourcecontrol.NewStackLifecycle(store, time.Now)
	require.NoError(t, err)

	stack, err := lifecycle.GetStack(ctx, "LEGACY", "epic:LEGACY-1")
	require.NoError(t, err)
	assert.Equal(t, "acme/legacy", stack.Repository)
	assert.Equal(t, "release/legacy", stack.RootBase)
	assert.Equal(t, sourcecontrol.CommitModeAgent, stack.DefaultCommitMode)
	assert.Equal(t, time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC), stack.CreatedAt)
	assert.Equal(t, time.Date(2026, 6, 2, 11, 0, 0, 0, time.UTC), stack.UpdatedAt)
	require.NoError(t, lifecycle.ValidateStack(ctx, "LEGACY", stack.ID))

	nodes, err := lifecycle.ListStackNodes(ctx, "LEGACY", stack.ID)
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.Equal(t, []string{"LEGACY-1", "LEGACY-2"}, []string{nodes[0].TaskID, nodes[1].TaskID})
	assert.Equal(t, sourcecontrol.CommitModeAgent, nodes[0].CommitMode)
	assert.Equal(t, sourcecontrol.NodeStatePublished, nodes[0].State)
	assert.Equal(t, 17, nodes[0].PRNumber)
	require.NotNil(t, nodes[0].LastPublishedAt)
	assert.Equal(t, time.Date(2026, 6, 2, 10, 30, 0, 0, time.UTC), *nodes[0].LastPublishedAt)
	assert.Equal(t, sourcecontrol.CommitModeSquash, nodes[1].CommitMode)
	assert.Equal(t, sourcecontrol.NodeStateConflicted, nodes[1].State)

	added, err := lifecycle.AddStackNode(ctx, sourcecontrol.AddStackNodeCommand{
		WorkspaceKey: "LEGACY",
		StackID:      stack.ID,
		TaskID:       "LEGACY-3",
	})
	require.NoError(t, err)
	assert.Equal(t, "LEGACY-2", added.BaseTaskID)
	assert.Equal(t, sourcecontrol.CommitModeAgent, added.CommitMode)
	assert.Equal(t, sourcecontrol.NodeStatePending, added.State)

	reloaded, err := sourcecontrol.NewStackLifecycle(stackstore.New(dir), time.Now)
	require.NoError(t, err)
	reloadedStack, err := reloaded.GetStack(ctx, "LEGACY", stack.ID)
	require.NoError(t, err)
	assert.Equal(t, "acme/legacy", reloadedStack.Repository)
	assert.Equal(t, sourcecontrol.CommitModeAgent, reloadedStack.DefaultCommitMode)
	assert.Equal(t, time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC), reloadedStack.CreatedAt)
	reloadedNodes, err := reloaded.ListStackNodes(ctx, "LEGACY", stack.ID)
	require.NoError(t, err)
	require.Len(t, reloadedNodes, 3)
	assert.Equal(t, sourcecontrol.CommitModeAgent, reloadedNodes[0].CommitMode)
	assert.Equal(t, sourcecontrol.NodeStatePublished, reloadedNodes[0].State)
	assert.Equal(t, 17, reloadedNodes[0].PRNumber)
	assert.Equal(t, "https://example.test/acme/legacy/pull/17", reloadedNodes[0].PRURL)
	assert.Equal(t, "1111111111111111111111111111111111111111", reloadedNodes[0].OutputSHA)
	require.NotNil(t, reloadedNodes[0].LastPublishedAt)
	assert.Equal(t, time.Date(2026, 6, 2, 10, 30, 0, 0, time.UTC), *reloadedNodes[0].LastPublishedAt)
	assert.Equal(t, sourcecontrol.CommitModeSquash, reloadedNodes[1].CommitMode)
	assert.Equal(t, sourcecontrol.NodeStateConflicted, reloadedNodes[1].State)
	assert.Equal(t, 18, reloadedNodes[1].PRNumber)
	assert.Equal(t, "LEGACY-2", reloadedNodes[2].BaseTaskID)

	persisted, err := os.ReadFile(filepath.Join(dir, "stacks.json"))
	require.NoError(t, err)
	assert.Contains(t, string(persisted), `"repoName": "acme/legacy"`)
	assert.NotContains(t, string(persisted), `"repository"`)
}
