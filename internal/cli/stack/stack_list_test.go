package stack

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

// The JSON output lists the same stacks as the text output: the declared ones
// and the per-repo Loom Git stacks Create PR or Approve recorded, so a
// cross-repo lead's stack ID is readable by scripts too.
func TestStackListJSONIncludesPublishedStacks(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "ws")
	store, err := stackstore.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureStack(context.Background(), sl.Stack{ID: "epic:E-1", WorkspaceKey: "ws", RepoName: "api", RootBase: "main"}); err != nil {
		t.Fatal(err)
	}
	previous := publishedStacks
	t.Cleanup(func() { publishedStacks = previous })
	publishedStacks = func(_ context.Context, workspace string) ([]publish.PublishedStack, error) {
		if workspace != "ws" {
			t.Fatalf("published stacks of %q, want ws", workspace)
		}
		return []publish.PublishedStack{{StackID: "lead-abc", Repo: "app", Slug: "owner/app",
			Layers: []publish.PublishedStackLayer{{Change: "c1", PRNumber: 7, PRURL: "https://example.test/7", Landed: true}}}}, nil
	}

	out := runListJSON(t)
	var entries []stackListEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %s, want the declared and the published stack", out)
	}
	if got := entries[0]; got.ID != "epic:E-1" || got.Repo != "api" || got.Source != "declared" || got.Base != "main" {
		t.Fatalf("declared entry = %+v", got)
	}
	got := entries[1]
	if got.ID != "lead-abc" || got.Repo != "app" || got.Source != "published" || got.Slug != "owner/app" ||
		len(got.Layers) != 1 || got.Layers[0].PRNumber != 7 || !got.Layers[0].Landed {
		t.Fatalf("published entry = %+v", got)
	}
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
