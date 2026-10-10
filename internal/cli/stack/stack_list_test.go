package stack

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

// The JSON output lists the same stacks as the text output: the declared ones
// and the per-repo Loom Git stacks Create PR or Approve recorded, so a
// cross-repo lead's stack ID is readable by scripts too.
func TestStackListJSONIncludesPublishedStacks(t *testing.T) {
	declared := declareStack(t)
	stubPublishedStacks(t, []publish.PublishedStack{{StackID: "lead-abc", Repo: "app", Slug: "owner/app",
		Layers: []publish.PublishedStackLayer{{Change: "c1", PRNumber: 7, PRURL: "https://example.test/7", Landed: true}}}})

	entries := listJSONEntries(t)
	if len(entries) != 2 {
		t.Fatalf("entries = %v, want the declared and the published stack", entries)
	}
	if entries[0]["id"] != string(declared.ID) || entries[0]["source"] != "declared" {
		t.Fatalf("declared entry = %v", entries[0])
	}
	got := entries[1]
	want := map[string]any{"id": "lead-abc", "workspaceKey": "ws", "repoName": "app", "source": "published", "slug": "owner/app",
		"layers": []any{map[string]any{"change": "c1", "pr_number": float64(7), "pr_url": "https://example.test/7", "landed": true}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("published entry = %v, want %v", got, want)
	}
}

// Declared stacks keep every key and value they had before published stacks
// were listed; the change only adds source.
func TestStackListJSONKeepsEveryDeclaredStackKey(t *testing.T) {
	declared := declareStack(t)
	stubPublishedStacks(t, nil)
	entries := listJSONEntries(t)
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want the declared stack", entries)
	}
	before, err := json.Marshal(declared)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(before, &want); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "workspaceKey", "repoName", "rootBase", "defaultCommitMode", "createdAt", "updatedAt"} {
		if _, ok := want[key]; !ok {
			t.Fatalf("stack record has no %q key: %s", key, before)
		}
	}
	want["source"] = "declared"
	if !reflect.DeepEqual(entries[0], want) {
		t.Fatalf("declared entry = %v, want the stack record plus source: %v", entries[0], want)
	}
}

func declareStack(t *testing.T) sl.Stack {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "ws")
	store, err := stackstore.Default()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.EnsureStack(ctx, sl.Stack{ID: "epic:E-1", WorkspaceKey: "ws", RepoName: "api", RootBase: "main",
		DefaultCommitMode: sl.CommitMode("loom_commit")}); err != nil {
		t.Fatal(err)
	}
	stacks, err := store.ListStacks(ctx, "ws")
	if err != nil || len(stacks) != 1 {
		t.Fatalf("declared stacks = %v, %v", stacks, err)
	}
	return stacks[0]
}

func stubPublishedStacks(t *testing.T, stacks []publish.PublishedStack) {
	t.Helper()
	previous := publishedStacks
	t.Cleanup(func() { publishedStacks = previous })
	publishedStacks = func(_ context.Context, workspace string) ([]publish.PublishedStack, error) {
		if workspace != "ws" {
			t.Fatalf("published stacks of %q, want ws", workspace)
		}
		return stacks, nil
	}
}

func listJSONEntries(t *testing.T) []map[string]any {
	t.Helper()
	out := runListJSON(t)
	var entries []map[string]any
	if err := json.Unmarshal(out, &entries); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	return entries
}

func TestStackListJSONIsAnEmptyListWithoutStacks(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "ws")
	previous := publishedStacks
	t.Cleanup(func() { publishedStacks = previous })
	publishedStacks = func(context.Context, string) ([]publish.PublishedStack, error) { return nil, nil }
	if out := string(runListJSON(t)); out != "[]\n" {
		t.Fatalf("output = %q, want []", out)
	}
}

func runListJSON(t *testing.T) []byte {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = write
	cmd := listCmd()
	cmd.SetArgs([]string{"--json"})
	runErr := cmd.ExecuteContext(context.Background())
	os.Stdout = stdout
	_ = write.Close()
	out, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("stack list --json: %v", runErr)
	}
	return out
}
