package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestDefaultFilesystemAndDepsAccessors(t *testing.T) {
	fs := defaultFileSystem{}
	dir := filepath.Join(t.TempDir(), "nested")
	if err := fs.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, "file.txt")
	if err := fs.WriteFile(path, []byte("content"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := fs.ReadFile(path)
	if err != nil || string(data) != "content" {
		t.Fatalf("ReadFile data=%q err=%v", data, err)
	}
	if info, err := fs.Stat(path); err != nil || info.IsDir() {
		t.Fatalf("Stat info=%v err=%v", info, err)
	}
	if err := fs.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	old := defaultDeps
	defaultDeps = &Deps{Clock: func() time.Time { return time.Unix(123, 0) }}
	t.Cleanup(func() { defaultDeps = old })
	if GetDeps(nil).Clock().Unix() != 123 {
		t.Fatal("GetDeps(nil) did not return test default deps")
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if GetDeps(cmd).Clock().Unix() != 123 {
		t.Fatal("GetDeps(command without deps) did not return test default deps")
	}
	custom := &Deps{Clock: func() time.Time { return time.Unix(456, 0) }}
	cmd.SetContext(WithDeps(context.Background(), custom))
	if GetDeps(cmd) != custom {
		t.Fatal("GetDeps did not return context deps")
	}
}

func TestUnavailableIssueBackendAllMethodsReturnUnavailable(t *testing.T) {
	ctx := context.Background()
	b := newUnavailableIssueBackend("test-backend", context.Canceled)
	check := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s returned nil error", name)
		}
		if !strings.Contains(err.Error(), "test-backend issue backend unavailable") {
			t.Fatalf("%s error = %v", name, err)
		}
	}

	_, err := b.Get(ctx, "ISSUE-1")
	check("Get", err)
	_, err = b.List(ctx, backend.ListOpts{})
	check("List", err)
	_, err = b.Ready(ctx, backend.ReadyOpts{})
	check("Ready", err)
	_, err = b.Blocked(ctx, backend.BlockedOpts{})
	check("Blocked", err)
	_, err = b.Stats(ctx)
	check("Stats", err)
	_, err = b.Count(ctx, backend.CountOpts{})
	check("Count", err)
	_, err = b.GetChildren(ctx, "ISSUE-1")
	check("GetChildren", err)
	_, err = b.SearchIssues(ctx, "query", 10)
	check("SearchIssues", err)
	_, err = b.Create(ctx, backend.CreateParams{Title: "new"})
	check("Create", err)
	check("Update", b.Update(ctx, "ISSUE-1", backend.UpdateParams{}))
	check("ClaimIssue", b.ClaimIssue(ctx, "ISSUE-1", time.Minute))
	check("DeferIssue", b.DeferIssue(ctx, "ISSUE-1", time.Now()))
	check("UndeferIssue", b.UndeferIssue(ctx, "ISSUE-1"))
	_, err = b.Close(ctx, "ISSUE-1", backend.CloseParams{})
	check("Close", err)
	check("Reopen", b.Reopen(ctx, "ISSUE-1", backend.ReopenParams{}))
	check("Delete", b.Delete(ctx, backend.DeleteParams{IDs: []string{"ISSUE-1"}}))
	check("AddDependency", b.AddDependency(ctx, backend.DepAddParams{FromID: "ISSUE-1", ToID: "ISSUE-2"}))
	check("RemoveDependency", b.RemoveDependency(ctx, backend.DepRemoveParams{FromID: "ISSUE-1", ToID: "ISSUE-2"}))
	check("AddLabel", b.AddLabel(ctx, "ISSUE-1", "bug"))
	check("RemoveLabel", b.RemoveLabel(ctx, "ISSUE-1", "bug"))
	_, err = b.ListComments(ctx, "ISSUE-1")
	check("ListComments", err)
	_, err = b.AddComment(ctx, backend.CommentAddParams{IssueID: "ISSUE-1", Text: "comment"})
	check("AddComment", err)
	_, err = b.ListEvents(ctx, "ISSUE-1", 10)
	check("ListEvents", err)
	_, err = b.Batch(ctx, []backend.BatchOp{{Operation: "noop"}})
	check("Batch", err)
	_, err = b.GetMutations(ctx, 0)
	check("GetMutations", err)
	_, err = b.WaitForMutations(ctx, 0, 1)
	check("WaitForMutations", err)

	if got := b.BackendName(); got != "test-backend-unavailable" {
		t.Fatalf("BackendName = %q", got)
	}
}

func TestFleetDBActorEnvPrecedence(t *testing.T) {
	t.Setenv("LOOM_FLEET_DB_ACTOR", "fleet-actor")
	t.Setenv("LOOM_AGENT_NAME", "agent-name")
	t.Setenv("USER", "user-name")
	if got := fleetDBActor(); got != "fleet-actor" {
		t.Fatalf("fleetDBActor fleet env = %q", got)
	}

	t.Setenv("LOOM_FLEET_DB_ACTOR", "")
	if got := fleetDBActor(); got != "agent-name" {
		t.Fatalf("fleetDBActor agent env = %q", got)
	}

	t.Setenv("LOOM_AGENT_NAME", "")
	if got := fleetDBActor(); got != "user-name" {
		t.Fatalf("fleetDBActor user env = %q", got)
	}
}
