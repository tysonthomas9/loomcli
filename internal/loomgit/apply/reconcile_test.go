package apply

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

func (f *fixture) interrupted(t *testing.T, phase string) (string, string) {
	t.Helper()
	ctx := context.Background()
	if err := f.store.SaveApplied(ctx, loomgit.AppliedLayer{
		RequestID: "apply-1", Workspace: "W", Lead: "L", Change: "C1", Revision: 1,
		OldTip: f.base, NewTip: f.source,
	}); err != nil {
		t.Fatal(err)
	}
	indexPath := f.git(t, "rev-parse", "--path-format=absolute", "--git-path", "index")
	lockPath := indexPath + ".lock"
	tmpPath, ownerPath := recoveryPaths(indexPath, "apply-1")
	if phase == "prepared" {
		return lockPath, ownerPath
	}
	if err := os.WriteFile(lockPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(lockPath, ownerPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AdvanceApplied(ctx, "apply-1", "prepared", "installing"); err != nil {
		t.Fatal(err)
	}
	if phase == "installing" {
		return lockPath, ownerPath
	}
	if _, err := f.runner.RunWithEnv(ctx, map[string]string{"GIT_INDEX_FILE": tmpPath},
		"read-tree", "-m", "-u", f.base, f.source); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AdvanceApplied(ctx, "apply-1", "installing", "files_updated"); err != nil {
		t.Fatal(err)
	}
	if phase == "files_updated" {
		return lockPath, ownerPath
	}
	f.git(t, "update-ref", "refs/heads/loom/ws/W/interactive/L", f.source, f.base)
	if err := f.store.AdvanceApplied(ctx, "apply-1", "files_updated", "ref_updated"); err != nil {
		t.Fatal(err)
	}
	return lockPath, ownerPath
}

func TestReconcileInterruptedApply(t *testing.T) {
	for _, phase := range []string{"prepared", "files_updated", "ref_updated"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t)
			f.write(t, "base", "user edit\n")
			lockPath, _ := f.interrupted(t, phase)
			if err := f.service.Reconcile(context.Background(), "W", "L"); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join(f.dir, "base"))
			if err != nil || string(body) != "user edit\n" {
				t.Fatalf("user edit changed: %q, %v", body, err)
			}
			if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("lock remains: %v", err)
			}
			log, err := f.store.AppliedLog(context.Background(), "W", "L")
			if err != nil {
				t.Fatal(err)
			}
			if phase == "prepared" {
				if len(log) != 0 || f.git(t, "rev-parse", "HEAD") != f.base {
					t.Fatal("unstarted apply was installed")
				}
				if _, err := f.apply(t); err != nil {
					t.Fatalf("apply retry: %v", err)
				}
			} else if len(log) != 1 || f.git(t, "rev-parse", "HEAD") != f.source ||
				!strings.Contains(f.git(t, "ls-files", "--stage"), f.git(t, "rev-parse", f.source+":change")) {
				t.Fatalf("swap not completed: %+v", log)
			}
		})
	}
}

func TestReconcileCASFailureKeepsEntryOpen(t *testing.T) {
	f := newFixture(t)
	f.interrupted(t, "files_updated")
	other := f.git(t, "commit-tree", f.git(t, "rev-parse", "HEAD^{tree}"), "-p", f.base, "-m", "other")
	f.service.beforeRecoverCAS = func() {
		f.git(t, "update-ref", "refs/heads/loom/ws/W/interactive/L", other, f.base)
	}
	if err := f.service.Reconcile(context.Background(), "W", "L"); err == nil {
		t.Fatal("concurrent ref move was accepted")
	}
	layers, err := f.store.OpenApplied(context.Background(), "W", "L")
	if err != nil || len(layers) != 1 || layers[0].Phase != "files_updated" {
		t.Fatalf("entry closed after CAS failure: %+v, %v", layers, err)
	}
	if f.git(t, "rev-parse", "HEAD") != other {
		t.Fatal("recovery overwrote concurrent ref")
	}
}

func TestReconcileAfterIndexRename(t *testing.T) {
	f := newFixture(t)
	lockPath, _ := f.interrupted(t, "ref_updated")
	indexPath := strings.TrimSuffix(lockPath, ".lock")
	tmpPath, _ := recoveryPaths(indexPath, "apply-1")
	if err := commitIndex(lockPath, indexPath, tmpPath); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Reconcile(context.Background(), "W", "L"); err != nil {
		t.Fatal(err)
	}
	log, err := f.store.AppliedLog(context.Background(), "W", "L")
	if err != nil || len(log) != 1 {
		t.Fatalf("renamed index did not finish: %+v, %v", log, err)
	}
}

func TestReconcileInstallingNeedsAttention(t *testing.T) {
	f := newFixture(t)
	f.write(t, "base", "user edit\n")
	lockPath, _ := f.interrupted(t, "installing")
	err := f.service.Reconcile(context.Background(), "W", "L")
	if !errors.Is(err, loomgit.NewError(loomgit.AttentionRequired, "", nil)) ||
		!strings.Contains(err.Error(), f.base) || !strings.Contains(err.Error(), f.source) {
		t.Fatalf("incomplete checkout update was accepted: %v", err)
	}
	body, readErr := os.ReadFile(filepath.Join(f.dir, "base"))
	if readErr != nil || string(body) != "user edit\n" || f.git(t, "rev-parse", "HEAD") != f.base {
		t.Fatalf("recovery changed user work: %q, %v", body, readErr)
	}
	if _, statErr := os.Stat(lockPath); statErr != nil {
		t.Fatalf("uncertain install lost its lock: %v", statErr)
	}
}

func TestReconcileRefMovedBeforePhaseAdvance(t *testing.T) {
	f := newFixture(t)
	f.interrupted(t, "files_updated")
	f.git(t, "update-ref", "refs/heads/loom/ws/W/interactive/L", f.source, f.base)
	if err := f.service.Reconcile(context.Background(), "W", "L"); err != nil {
		t.Fatal(err)
	}
	log, err := f.store.AppliedLog(context.Background(), "W", "L")
	if err != nil || len(log) != 1 || f.git(t, "rev-parse", "HEAD") != f.source {
		t.Fatalf("ref transition was not completed: %+v, %v", log, err)
	}
}

func TestReconcileRejectsForeignLockAndMovedHead(t *testing.T) {
	for _, tc := range []struct {
		name, phase string
		change      func(*testing.T, *fixture, string, string)
	}{
		{"foreign lock", "prepared", func(t *testing.T, _ *fixture, lockPath, _ string) {
			if err := os.WriteFile(lockPath, []byte("foreign"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"moved HEAD", "files_updated", func(t *testing.T, f *fixture, _, _ string) {
			f.git(t, "update-ref", "refs/heads/loom/ws/W/interactive/L", f.source, f.base)
			f.git(t, "update-ref", "refs/heads/loom/ws/W/interactive/L", f.base, f.source)
			other := f.git(t, "commit-tree", f.git(t, "rev-parse", "HEAD^{tree}"), "-p", f.base, "-m", "other")
			f.git(t, "update-ref", "refs/heads/loom/ws/W/interactive/L", other, f.base)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			lockPath, ownerPath := f.interrupted(t, tc.phase)
			tc.change(t, f, lockPath, ownerPath)
			before := f.git(t, "rev-parse", "HEAD")
			err := f.service.Reconcile(context.Background(), "W", "L")
			if !errors.Is(err, loomgit.NewError(loomgit.AttentionRequired, "", nil)) ||
				!strings.Contains(err.Error(), f.base) || !strings.Contains(err.Error(), f.source) {
				t.Fatalf("expected named attention: %v", err)
			}
			if f.git(t, "rev-parse", "HEAD") != before {
				t.Fatal("recovery moved HEAD")
			}
		})
	}
}
