package driver

import (
	"context"
	"testing"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

func TestStackOutcome(t *testing.T) {
	cases := []struct {
		name      string
		meta      map[string]string
		wantState sl.NodeState
		wantSHA   string
		wantOK    bool
	}{
		{"pr delivery by branch", map[string]string{"github_branch": "loom/stack/epic:E/A", "github_head_sha": "abc123"}, sl.NodeStatePublished, "abc123", true},
		{"pr delivery by delivery key", map[string]string{"delivery": "pull_request"}, sl.NodeStatePublished, "", true},
		{"empty diff skipped", map[string]string{"delivery": "pull_request_skipped_no_changes"}, sl.NodeStateEmpty, "", true},
		{"empty by files_changed", map[string]string{"files_changed": "0"}, sl.NodeStateEmpty, "", true},
		{"patch back is not a stacked outcome", map[string]string{"delivery": "patch_back"}, "", "", false},
		{"nil metadata", nil, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, sha, ok := stackOutcome(tc.meta)
			if state != tc.wantState || sha != tc.wantSHA || ok != tc.wantOK {
				t.Fatalf("stackOutcome = (%q,%q,%v), want (%q,%q,%v)", state, sha, ok, tc.wantState, tc.wantSHA, tc.wantOK)
			}
		})
	}
}

// TestFinalizeBarrierEnablesDependentBase is the Stage-2 verify bar, offline: a
// 2-task chain where, once task A's finalize barrier records it published, task
// B's worktree-base lookup resolves to A's branch (not the repo default).
func TestFinalizeBarrierEnablesDependentBase(t *testing.T) {
	ctx := context.Background()
	store := stackstore.New(t.TempDir())
	const ws, repo = "WS", "acme/widgets"

	if err := store.EnsureStack(ctx, sl.Stack{ID: "epic:E", WorkspaceKey: ws, RepoName: repo, RootBase: "main"}); err != nil {
		t.Fatalf("ensure stack: %v", err)
	}
	if _, err := store.AddNode(ctx, ws, "epic:E", "A", "", ""); err != nil {
		t.Fatalf("add A: %v", err)
	}
	if _, err := store.AddNode(ctx, ws, "epic:E", "B", "A", ""); err != nil {
		t.Fatalf("add B: %v", err)
	}
	lookup := StackLineageLookup{Store: store}
	aBranch := sl.OutputBranchName("epic:E", "A")

	// Before A finishes: A is pending (no branch pushed yet). B still resolves to
	// A's assigned branch name — AssignBranch sets it at registration, and the
	// finalize barrier is what guarantees that branch exists by dispatch time.
	if ref, ok, err := lookup.BaseRefForTask(ctx, ws, repo, "B"); err != nil || !ok || ref != aBranch {
		t.Fatalf("pre-finalize B base = (%q,%v,%v), want (%q,true,nil)", ref, ok, err, aBranch)
	}

	// Finalize barrier records A published (the worker would record this before
	// closing A and unblocking B).
	recorded, err := recordStackOutput(ctx, store, ws, repo, "A", sl.NodeStatePublished, "deadbeef")
	if err != nil || !recorded {
		t.Fatalf("recordStackOutput(A) = (%v,%v), want (true,nil)", recorded, err)
	}
	nodes, _ := store.ListNodes(ctx, ws, "epic:E")
	for _, n := range nodes {
		if n.TaskID == "A" {
			if n.State != sl.NodeStatePublished || n.OutputSHA != "deadbeef" {
				t.Fatalf("A node after finalize = %+v, want published/deadbeef", n)
			}
			if n.OutputBranch != aBranch {
				t.Fatalf("finalize must not reassign OutputBranch: got %q want %q", n.OutputBranch, aBranch)
			}
		}
	}

	// After A is published, B still bases on A's branch — now durably backed.
	if ref, ok, err := lookup.BaseRefForTask(ctx, ws, repo, "B"); err != nil || !ok || ref != aBranch {
		t.Fatalf("post-finalize B base = (%q,%v,%v), want (%q,true,nil)", ref, ok, err, aBranch)
	}

	// An empty-diff A slides B down to RootBase (decision (a)).
	if _, err := recordStackOutput(ctx, store, ws, repo, "A", sl.NodeStateEmpty, ""); err != nil {
		t.Fatalf("record A empty: %v", err)
	}
	if ref, ok, err := lookup.BaseRefForTask(ctx, ws, repo, "B"); err != nil || !ok || ref != "main" {
		t.Fatalf("empty-A B base = (%q,%v,%v), want (main,true,nil)", ref, ok, err)
	}
}

func TestRecordStackOutputNoStackIsNoop(t *testing.T) {
	ctx := context.Background()
	store := stackstore.New(t.TempDir())
	// No stack for this repo/task → no-op, no error.
	recorded, err := recordStackOutput(ctx, store, "WS", "acme/widgets", "ghost", sl.NodeStatePublished, "")
	if err != nil || recorded {
		t.Fatalf("recordStackOutput(no stack) = (%v,%v), want (false,nil)", recorded, err)
	}
	// Nil store is inert.
	if recorded, err := recordStackOutput(ctx, nil, "WS", "acme/widgets", "A", sl.NodeStatePublished, ""); err != nil || recorded {
		t.Fatalf("recordStackOutput(nil store) = (%v,%v), want (false,nil)", recorded, err)
	}
}
