package cli

import (
	"fmt"
	"sync"
	"testing"
)

// stubQueryOpenEpics replaces queryOpenEpics for the duration of the test.
func stubQueryOpenEpics(t *testing.T, fn func() ([]EpicInfo, error)) {
	t.Helper()
	orig := queryOpenEpics
	queryOpenEpics = fn
	t.Cleanup(func() { queryOpenEpics = orig })
}

// stubEpicHasReadyTasks replaces epicHasReadyTasks for the duration of the test.
func stubEpicHasReadyTasks(t *testing.T, fn func(string) (bool, error)) {
	t.Helper()
	orig := epicHasReadyTasks
	epicHasReadyTasks = fn
	t.Cleanup(func() { epicHasReadyTasks = orig })
}

func TestEpicAssigner_AssignWorktree(t *testing.T) {
	t.Run("no epics returns empty string", func(t *testing.T) {
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return nil, nil
		})
		stubEpicHasReadyTasks(t, func(string) (bool, error) {
			t.Fatal("should not be called when no epics")
			return false, nil
		})

		ea := NewEpicAssigner()
		epicID, err := ea.AssignWorktree("falcon")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if epicID != "" {
			t.Errorf("expected empty epicID, got %q", epicID)
		}
	})

	t.Run("one epic with ready tasks assigns it", func(t *testing.T) {
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return []EpicInfo{{ID: "epic-1", Priority: 1}}, nil
		})
		stubEpicHasReadyTasks(t, func(id string) (bool, error) {
			return id == "epic-1", nil
		})

		ea := NewEpicAssigner()
		epicID, err := ea.AssignWorktree("falcon")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if epicID != "epic-1" {
			t.Errorf("expected epic-1, got %q", epicID)
		}
	})

	t.Run("multiple worktrees get different epics", func(t *testing.T) {
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return []EpicInfo{
				{ID: "epic-1", Priority: 1},
				{ID: "epic-2", Priority: 2},
			}, nil
		})
		stubEpicHasReadyTasks(t, func(id string) (bool, error) {
			return true, nil
		})

		ea := NewEpicAssigner()
		id1, err := ea.AssignWorktree("falcon")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		id2, err := ea.AssignWorktree("nova")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if id1 == id2 {
			t.Errorf("both worktrees assigned to same epic %q", id1)
		}
		if id1 != "epic-1" {
			t.Errorf("falcon expected epic-1 (highest priority), got %q", id1)
		}
		if id2 != "epic-2" {
			t.Errorf("nova expected epic-2, got %q", id2)
		}
	})

	t.Run("already assigned worktree returns same epic", func(t *testing.T) {
		callCount := 0
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			callCount++
			return []EpicInfo{{ID: "epic-1", Priority: 1}}, nil
		})
		stubEpicHasReadyTasks(t, func(string) (bool, error) {
			return true, nil
		})

		ea := NewEpicAssigner()
		id1, _ := ea.AssignWorktree("falcon")
		id2, _ := ea.AssignWorktree("falcon")

		if id1 != id2 {
			t.Errorf("expected same assignment, got %q and %q", id1, id2)
		}
		// Should only query epics once (second call returns cached assignment)
		if callCount != 1 {
			t.Errorf("expected 1 query, got %d", callCount)
		}
	})

	t.Run("priority ordering P0 before P1 before P2", func(t *testing.T) {
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return []EpicInfo{
				{ID: "epic-p2", Priority: 2},
				{ID: "epic-p0", Priority: 0},
				{ID: "epic-p1", Priority: 1},
			}, nil
		})
		stubEpicHasReadyTasks(t, func(string) (bool, error) {
			return true, nil
		})

		ea := NewEpicAssigner()
		id, _ := ea.AssignWorktree("falcon")
		if id != "epic-p0" {
			t.Errorf("expected epic-p0 (highest priority), got %q", id)
		}
	})

	t.Run("epics without ready tasks are skipped", func(t *testing.T) {
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return []EpicInfo{
				{ID: "epic-empty", Priority: 0},
				{ID: "epic-ready", Priority: 1},
			}, nil
		})
		stubEpicHasReadyTasks(t, func(id string) (bool, error) {
			return id == "epic-ready", nil
		})

		ea := NewEpicAssigner()
		id, _ := ea.AssignWorktree("falcon")
		if id != "epic-ready" {
			t.Errorf("expected epic-ready, got %q", id)
		}
	})

	t.Run("more worktrees than epics returns empty for extras", func(t *testing.T) {
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return []EpicInfo{{ID: "epic-1", Priority: 1}}, nil
		})
		stubEpicHasReadyTasks(t, func(string) (bool, error) {
			return true, nil
		})

		ea := NewEpicAssigner()
		id1, _ := ea.AssignWorktree("falcon")
		id2, _ := ea.AssignWorktree("nova")

		if id1 != "epic-1" {
			t.Errorf("falcon expected epic-1, got %q", id1)
		}
		if id2 != "" {
			t.Errorf("nova expected empty (no more epics), got %q", id2)
		}
	})

	t.Run("query failure returns error", func(t *testing.T) {
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return nil, fmt.Errorf("bd command failed")
		})
		stubEpicHasReadyTasks(t, func(string) (bool, error) {
			return false, nil
		})

		ea := NewEpicAssigner()
		_, err := ea.AssignWorktree("falcon")
		if err == nil {
			t.Fatal("expected error when query fails")
		}
	})

	t.Run("ready check failure skips that epic", func(t *testing.T) {
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return []EpicInfo{
				{ID: "epic-err", Priority: 0},
				{ID: "epic-ok", Priority: 1},
			}, nil
		})
		stubEpicHasReadyTasks(t, func(id string) (bool, error) {
			if id == "epic-err" {
				return false, fmt.Errorf("check failed")
			}
			return true, nil
		})

		ea := NewEpicAssigner()
		id, err := ea.AssignWorktree("falcon")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "epic-ok" {
			t.Errorf("expected epic-ok (skipped erroring epic), got %q", id)
		}
	})
}

func TestEpicAssigner_ReleaseWorktree(t *testing.T) {
	t.Run("release makes epic available again", func(t *testing.T) {
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return []EpicInfo{{ID: "epic-1", Priority: 1}}, nil
		})
		stubEpicHasReadyTasks(t, func(string) (bool, error) {
			return true, nil
		})

		ea := NewEpicAssigner()

		// Assign falcon to epic-1
		id1, _ := ea.AssignWorktree("falcon")
		if id1 != "epic-1" {
			t.Fatalf("expected epic-1, got %q", id1)
		}

		// nova gets nothing (epic-1 is taken)
		id2, _ := ea.AssignWorktree("nova")
		if id2 != "" {
			t.Fatalf("expected empty, got %q", id2)
		}

		// Release falcon
		ea.ReleaseWorktree("falcon")

		// Now nova can get epic-1
		id3, _ := ea.AssignWorktree("nova")
		if id3 != "epic-1" {
			t.Errorf("expected epic-1 after release, got %q", id3)
		}
	})

	t.Run("release non-existent worktree is no-op", func(t *testing.T) {
		ea := NewEpicAssigner()
		ea.ReleaseWorktree("nonexistent") // should not panic
	})
}

func TestEpicAssigner_ReassignAfterRelease(t *testing.T) {
	t.Run("reassigns to higher-priority epic after release", func(t *testing.T) {
		// Start with two epics: epic-1 (P1) and epic-2 (P2)
		epics := []EpicInfo{
			{ID: "epic-1", Priority: 1},
			{ID: "epic-2", Priority: 2},
		}
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return epics, nil
		})
		stubEpicHasReadyTasks(t, func(string) (bool, error) {
			return true, nil
		})

		ea := NewEpicAssigner()

		// Assign falcon to epic-1 (highest priority)
		id1, err := ea.AssignWorktree("falcon")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id1 != "epic-1" {
			t.Fatalf("expected epic-1, got %q", id1)
		}

		// Release falcon
		ea.ReleaseWorktree("falcon")

		// Now a higher-priority epic appears
		epics = []EpicInfo{
			{ID: "epic-0", Priority: 0},
			{ID: "epic-1", Priority: 1},
			{ID: "epic-2", Priority: 2},
		}

		// Reassign — should get epic-0 (new highest priority), not epic-1
		id2, err := ea.AssignWorktree("falcon")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id2 != "epic-0" {
			t.Errorf("expected epic-0 after release, got %q", id2)
		}
	})

	t.Run("reassigns to next epic when current is exhausted", func(t *testing.T) {
		exhausted := map[string]bool{}
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return []EpicInfo{
				{ID: "epic-1", Priority: 1},
				{ID: "epic-2", Priority: 2},
			}, nil
		})
		stubEpicHasReadyTasks(t, func(id string) (bool, error) {
			return !exhausted[id], nil
		})

		ea := NewEpicAssigner()

		// Assign to epic-1
		id1, err := ea.AssignWorktree("falcon")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id1 != "epic-1" {
			t.Fatalf("expected epic-1, got %q", id1)
		}

		// Release and mark epic-1 as exhausted
		ea.ReleaseWorktree("falcon")
		exhausted["epic-1"] = true

		// Reassign — should get epic-2 (epic-1 has no ready tasks)
		id2, err := ea.AssignWorktree("falcon")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id2 != "epic-2" {
			t.Errorf("expected epic-2 after exhaustion, got %q", id2)
		}
	})

	t.Run("release and reassign with no epics returns empty", func(t *testing.T) {
		callCount := 0
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			callCount++
			if callCount == 1 {
				return []EpicInfo{{ID: "epic-1", Priority: 1}}, nil
			}
			return nil, nil // no epics on second call
		})
		stubEpicHasReadyTasks(t, func(string) (bool, error) {
			return true, nil
		})

		ea := NewEpicAssigner()

		// Assign to epic-1
		id1, _ := ea.AssignWorktree("falcon")
		if id1 != "epic-1" {
			t.Fatalf("expected epic-1, got %q", id1)
		}

		// Release
		ea.ReleaseWorktree("falcon")

		// Reassign — no epics available
		id2, err := ea.AssignWorktree("falcon")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id2 != "" {
			t.Errorf("expected empty after all epics gone, got %q", id2)
		}
	})
}

func TestEpicAssigner_GetAssignment(t *testing.T) {
	t.Run("returns epic ID for assigned worktree", func(t *testing.T) {
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return []EpicInfo{{ID: "epic-1", Priority: 1}}, nil
		})
		stubEpicHasReadyTasks(t, func(string) (bool, error) {
			return true, nil
		})

		ea := NewEpicAssigner()
		ea.AssignWorktree("falcon")

		if got := ea.GetAssignment("falcon"); got != "epic-1" {
			t.Errorf("GetAssignment(falcon) = %q, want epic-1", got)
		}
	})

	t.Run("returns empty for unassigned worktree", func(t *testing.T) {
		ea := NewEpicAssigner()
		if got := ea.GetAssignment("falcon"); got != "" {
			t.Errorf("GetAssignment(falcon) = %q, want empty", got)
		}
	})
}

func TestEpicAssigner_Assignments(t *testing.T) {
	t.Run("returns copy of assignment map", func(t *testing.T) {
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return []EpicInfo{
				{ID: "epic-1", Priority: 1},
				{ID: "epic-2", Priority: 2},
			}, nil
		})
		stubEpicHasReadyTasks(t, func(string) (bool, error) {
			return true, nil
		})

		ea := NewEpicAssigner()
		ea.AssignWorktree("falcon")
		ea.AssignWorktree("nova")

		assignments := ea.Assignments()
		if len(assignments) != 2 {
			t.Fatalf("expected 2 assignments, got %d", len(assignments))
		}
		if assignments["falcon"] != "epic-1" {
			t.Errorf("falcon = %q, want epic-1", assignments["falcon"])
		}
		if assignments["nova"] != "epic-2" {
			t.Errorf("nova = %q, want epic-2", assignments["nova"])
		}

		// Verify it's a copy (modifying returned map doesn't affect internal state)
		delete(assignments, "falcon")
		if ea.GetAssignment("falcon") != "epic-1" {
			t.Error("modifying returned map affected internal state")
		}
	})
}

func TestEpicAssigner_ConcurrentAccess(t *testing.T) {
	t.Run("concurrent AssignWorktree is safe", func(t *testing.T) {
		stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
			return []EpicInfo{
				{ID: "epic-1", Priority: 1},
				{ID: "epic-2", Priority: 2},
				{ID: "epic-3", Priority: 3},
				{ID: "epic-4", Priority: 4},
			}, nil
		})
		stubEpicHasReadyTasks(t, func(string) (bool, error) {
			return true, nil
		})

		ea := NewEpicAssigner()
		var wg sync.WaitGroup
		results := make(map[string]string)
		var mu sync.Mutex

		worktrees := []string{"wt-1", "wt-2", "wt-3", "wt-4"}
		for _, wt := range worktrees {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				id, err := ea.AssignWorktree(name)
				if err != nil {
					t.Errorf("AssignWorktree(%s) error: %v", name, err)
					return
				}
				mu.Lock()
				results[name] = id
				mu.Unlock()
			}(wt)
		}
		wg.Wait()

		// Verify no two worktrees got the same non-empty epic
		epicToWT := make(map[string]string)
		for wt, epic := range results {
			if epic == "" {
				continue
			}
			if prev, ok := epicToWT[epic]; ok {
				t.Errorf("epic %s assigned to both %s and %s", epic, prev, wt)
			}
			epicToWT[epic] = wt
		}
	})
}
