package bootstrap

import "testing"

func seedTwoWorkspaces(t *testing.T) {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	if err := MutateStateCache(func(sc *StateCache) error {
		sc.Workspaces["A"] = WorkspaceLocalState{Path: "/tmp/a"}
		sc.Workspaces["B"] = WorkspaceLocalState{Path: "/tmp/b"}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// Regression: adding a workspace must merge with the
// on-disk map, never replace it (the original bug saved a fresh struct
// holding only the new workspace, dropping all others).
func TestMutateStateCachePreservesExistingWorkspaces(t *testing.T) {
	seedTwoWorkspaces(t)

	if err := MutateStateCache(func(sc *StateCache) error {
		sc.Workspaces["C"] = WorkspaceLocalState{Path: "/tmp/c"}
		return nil
	}); err != nil {
		t.Fatalf("MutateStateCache: %v", err)
	}

	sc, err := LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache: %v", err)
	}
	for _, key := range []string{"A", "B", "C"} {
		if _, ok := sc.Workspaces[key]; !ok {
			t.Fatalf("workspace %q missing after mutate; got %v", key, sc.Workspaces)
		}
	}
}

// Deletions inside the mutate fn must stick — read-modify-write must
// not resurrect deleted entries from disk.
func TestMutateStateCacheDeleteDoesNotResurrect(t *testing.T) {
	seedTwoWorkspaces(t)

	if err := MutateStateCache(func(sc *StateCache) error {
		delete(sc.Workspaces, "A")
		return nil
	}); err != nil {
		t.Fatalf("MutateStateCache: %v", err)
	}

	sc, err := LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache: %v", err)
	}
	if _, ok := sc.Workspaces["A"]; ok {
		t.Fatal("workspace A resurrected after delete")
	}
	if _, ok := sc.Workspaces["B"]; !ok {
		t.Fatal("workspace B lost during delete of A")
	}
}

func TestMutateStateCacheNilFn(t *testing.T) {
	if err := MutateStateCache(nil); err == nil {
		t.Fatal("MutateStateCache(nil) = nil error, want error")
	}
}
