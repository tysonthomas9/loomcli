package worktreegroups

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const testWorkspaceID = "test-ws-uuid"

func setupTest(t *testing.T) (*Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewStore(rdb, nil), mr
}

func testGroup(name string) TerminalWorktreeGroup {
	return TerminalWorktreeGroup{
		ID:   "group-" + name,
		Name: name,
		Root: "/workspace/.loom/terminal-worktrees/" + name,
		Members: []WorktreeGroupMember{
			{
				RepoName:     "loomcli",
				Path:         "/workspace/.loom/terminal-worktrees/" + name + "/loomcli",
				BaseBranch:   "main",
				BaseDetached: false,
				ReusedBranch: true,
			},
		},
		CreatedAt: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
	}
}

func TestAddListGetRoundTrip(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	group := testGroup("feature-auth")
	if err := store.Add(ctx, testWorkspaceID, group); err != nil {
		t.Fatalf("Add: %v", err)
	}

	groups, err := store.List(ctx, testWorkspaceID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("len(groups) = %d, want 1", len(groups))
	}
	if groups[0].Name != "feature-auth" {
		t.Fatalf("groups[0].Name = %q, want feature-auth", groups[0].Name)
	}
	if len(groups[0].Members) != 1 {
		t.Fatalf("len(groups[0].Members) = %d, want 1", len(groups[0].Members))
	}
	if !groups[0].Members[0].ReusedBranch {
		t.Fatalf("groups[0].Members[0].ReusedBranch = false, want true")
	}

	got, err := store.Get(ctx, testWorkspaceID, "feature-auth")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil, want group")
	}
	if got.ID != group.ID {
		t.Fatalf("Get().ID = %q, want %q", got.ID, group.ID)
	}

	if mr.TTL(workspaceKey(testWorkspaceID)) != 0 {
		t.Fatalf("worktree group key has TTL, want no TTL")
	}
}

func TestListGetUnknownWorkspace(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	groups, err := store.List(ctx, testWorkspaceID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if groups == nil {
		t.Fatal("List returned nil, want empty slice")
	}
	if len(groups) != 0 {
		t.Fatalf("len(groups) = %d, want 0", len(groups))
	}

	group, err := store.Get(ctx, testWorkspaceID, "missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if group != nil {
		t.Fatalf("Get returned %+v, want nil", group)
	}
}

func TestAddFailsOnExistingName(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	if err := store.Add(ctx, testWorkspaceID, testGroup("feature-auth")); err != nil {
		t.Fatalf("Add first group: %v", err)
	}
	err := store.Add(ctx, testWorkspaceID, TerminalWorktreeGroup{
		ID:        "different-id",
		Name:      "feature-auth",
		Root:      "/other/root",
		CreatedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrGroupExists) {
		t.Fatalf("Add duplicate error = %v, want ErrGroupExists", err)
	}
}

func TestWithWorkspaceLockConcurrentCreateSecondConflicts(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	create := func() error {
		return store.WithWorkspaceLock(testWorkspaceID, func(locked *LockedWorkspace) error {
			existing, err := locked.Get(ctx, "feature-auth")
			if err != nil {
				return err
			}
			if existing != nil {
				return ErrGroupExists
			}
			return locked.Add(ctx, testGroup("feature-auth"))
		})
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- create()
		}()
	}
	wg.Wait()
	close(errs)

	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrGroupExists):
			conflicts++
		default:
			t.Fatalf("unexpected create error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1 success and 1 conflict", successes, conflicts)
	}
}

func TestDeleteWorkspace(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	if err := store.Add(ctx, testWorkspaceID, testGroup("feature-auth")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !mr.Exists(workspaceKey(testWorkspaceID)) {
		t.Fatalf("expected redis key to exist before delete")
	}

	if err := store.DeleteWorkspace(ctx, testWorkspaceID); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	if mr.Exists(workspaceKey(testWorkspaceID)) {
		t.Fatalf("redis key still exists after DeleteWorkspace")
	}

	groups, err := store.List(ctx, testWorkspaceID)
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("len(groups) after delete = %d, want 0", len(groups))
	}
}
