package stackpublish

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sl "github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol/stacklineage"
)

func TestChains(t *testing.T) {
	ordered := []sl.Node{
		{TaskID: "A1"}, {TaskID: "A2", BaseTaskID: "A1"},
		{TaskID: "B1"}, {TaskID: "B2", BaseTaskID: "B1"}, {TaskID: "B3", BaseTaskID: "B2"},
	}
	got := chains(ordered)
	require.Len(t, got, 2)
	assert.Equal(t, []string{"A1", "A2"}, taskIDs(got[0]))
	assert.Equal(t, []string{"B1", "B2", "B3"}, taskIDs(got[1]))
}

func taskIDs(ns []sl.Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.TaskID
	}
	return out
}

type fakeResolver struct {
	calls     int
	resolveTo string
}

func (f *fakeResolver) ResolveRebaseConflicts(_ context.Context, repoPath, _, _ string, conflicts []string) error {
	f.calls++
	for _, c := range conflicts {
		_ = os.WriteFile(filepath.Join(repoPath, c), []byte(f.resolveTo), 0o644)
	}
	return nil
}

// conflictRepo builds a repo where rebasing T2 onto main (after main changed the
// same file T2 touches) conflicts. Returns the repo dir and T1's tip (the cut point).
func conflictRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	g := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput() //nolint:norawexec
		require.NoErrorf(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	w := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	g("init", "-q")
	g("config", "user.email", "t@t")
	g("config", "user.name", "t")
	w("f.txt", "base\n")
	g("add", "-A")
	g("commit", "-q", "-m", "base")
	g("branch", "-M", "main")

	g("checkout", "-q", "-b", "loom/stack/s/T1", "main")
	w("f.txt", "T1\n")
	g("add", "-A")
	g("commit", "-q", "-m", "T1")
	t1tip := g("rev-parse", "HEAD")

	g("checkout", "-q", "-b", "loom/stack/s/T2", "loom/stack/s/T1")
	w("f.txt", "T1\nT2\n")
	g("add", "-A")
	g("commit", "-q", "-m", "T2")

	// main diverges on the same file (as a squash-merge of T1 would).
	g("checkout", "-q", "main")
	w("f.txt", "MAIN\n")
	g("add", "-A")
	g("commit", "-q", "-m", "squash of T1")
	return dir, t1tip
}

func TestRebaseOnto_ConflictResolvedByAgent(t *testing.T) {
	ctx := context.Background()
	dir, t1tip := conflictRepo(t)
	res := &fakeResolver{resolveTo: "RESOLVED\n"}

	resolved, err := (&Reconciler{}).rebaseOnto(ctx, dir, "loom/stack/s/T2", "main", t1tip, res)
	require.NoError(t, err)
	assert.True(t, resolved, "the rebase conflicted and was resolved")
	assert.GreaterOrEqual(t, res.calls, 1)

	// T2 is now based on main (main is an ancestor of T2).
	ok, err := isAncestor(ctx, dir, "main", "loom/stack/s/T2")
	require.NoError(t, err)
	assert.True(t, ok, "T2 rebased onto main")
}

func TestRebaseOnto_NoResolverFailsClosed(t *testing.T) {
	ctx := context.Background()
	dir, t1tip := conflictRepo(t)

	_, err := (&Reconciler{}).rebaseOnto(ctx, dir, "loom/stack/s/T2", "main", t1tip, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no resolver")

	// The failed rebase was aborted — the repo is not left mid-rebase.
	st, _ := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput() //nolint:norawexec
	assert.NotContains(t, string(st), "UU ", "rebase aborted, no unmerged paths")
}

func TestRebaseOnto_CleanNoConflict(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	g := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput() //nolint:norawexec
		require.NoErrorf(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	w := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	g("init", "-q")
	g("config", "user.email", "t@t")
	g("config", "user.name", "t")
	w("f.txt", "base\n")
	g("add", "-A")
	g("commit", "-q", "-m", "base")
	g("branch", "-M", "main")
	g("checkout", "-q", "-b", "loom/stack/s/T1", "main")
	w("f.txt", "T1\n")
	g("add", "-A")
	g("commit", "-q", "-m", "T1")
	t1tip := g("rev-parse", "HEAD")
	g("checkout", "-q", "-b", "loom/stack/s/T2", "loom/stack/s/T1")
	w("g.txt", "T2-only\n") // different file → no conflict with main's f.txt change
	g("add", "-A")
	g("commit", "-q", "-m", "T2")
	g("checkout", "-q", "main")
	w("f.txt", "MAIN\n")
	g("add", "-A")
	g("commit", "-q", "-m", "squash of T1")

	res := &fakeResolver{resolveTo: "x"}
	resolved, err := (&Reconciler{}).rebaseOnto(ctx, dir, "loom/stack/s/T2", "main", t1tip, res)
	require.NoError(t, err)
	assert.False(t, resolved, "no conflict → resolver not invoked")
	assert.Equal(t, 0, res.calls)
}
