package stackpublish

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

func remoteRef(t *testing.T, origin, ref string) string {
	t.Helper()
	return strings.TrimSpace(git(t, filepath.Dir(origin), "--git-dir", origin, "rev-parse", ref))
}

func setupLocalOrigin(t *testing.T) (root, origin, work string) {
	t.Helper()
	root = t.TempDir()
	origin = filepath.Join(root, "origin.git")
	work = filepath.Join(root, "work")
	git(t, root, "init", "-q", "--bare", "--initial-branch=main", origin)
	git(t, root, "clone", "-q", origin, work)
	git(t, work, "commit", "-q", "--allow-empty", "-m", "root")
	git(t, work, "branch", "-M", "main")
	git(t, work, "push", "-q", "origin", "main")
	return root, origin, work
}

func TestNewForgeForOriginStrictSelection(t *testing.T) {
	_, localOrigin, _ := setupLocalOrigin(t)
	fileURL := "file://" + localOrigin

	tests := []struct {
		name    string
		origin  string
		kind    OriginKind
		owner   string
		repo    string
		wantErr bool
	}{
		{name: "github https", origin: "https://github.com/acme/widgets.git", kind: OriginKindGitHub, owner: "acme", repo: "widgets"},
		{name: "github ssh url", origin: "ssh://git@github.com/acme/widgets.git", kind: OriginKindGitHub, owner: "acme", repo: "widgets"},
		{name: "github scp", origin: "git@github.com:acme/widgets.git", kind: OriginKindGitHub, owner: "acme", repo: "widgets"},
		{name: "absolute path", origin: localOrigin, kind: OriginKindLocal},
		{name: "file url", origin: fileURL, kind: OriginKindLocal},
		{name: "empty", origin: "", wantErr: true},
		{name: "non github https", origin: "https://gitlab.example/acme/widgets.git", wantErr: true},
		{name: "relative path", origin: "relative/origin.git", wantErr: true},
		{name: "remote file host", origin: "file://example.com/tmp/origin.git", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forge, origin, err := NewForgeForOrigin(tt.origin, "tok")
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "stackpublish: cannot parse owner/repo from origin")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.kind, origin.Kind)
			assert.Equal(t, tt.owner, origin.Owner)
			assert.Equal(t, tt.repo, origin.Repo)
			switch tt.kind {
			case OriginKindGitHub:
				_, ok := forge.(*GitHubForge)
				assert.True(t, ok, "github origins must select GitHubForge")
			case OriginKindLocal:
				_, ok := forge.(*LocalForge)
				assert.True(t, ok, "filesystem origins must select LocalForge")
			}
		})
	}
}

func TestLocalForgePushBranchesForceWithLease(t *testing.T) {
	ctx := context.Background()
	_, origin, work := setupLocalOrigin(t)
	branch := "loom/local/T1"

	git(t, work, "checkout", "-q", "-B", branch, "main")
	git(t, work, "commit", "-q", "--allow-empty", "-m", "first")
	first := strings.TrimSpace(git(t, work, "rev-parse", branch))

	forge := NewLocalForge(origin)
	require.NoError(t, forge.PushBranches(ctx, work, []BranchPush{{Branch: branch}}))
	assert.Equal(t, first, remoteRef(t, origin, "refs/heads/"+branch))

	git(t, work, "reset", "--hard", "main")
	git(t, work, "commit", "-q", "--allow-empty", "-m", "rewrite")
	second := strings.TrimSpace(git(t, work, "rev-parse", branch))
	require.NoError(t, forge.PushBranches(ctx, work, []BranchPush{{Branch: branch, ExpectedSHA: first}}))
	assert.Equal(t, second, remoteRef(t, origin, "refs/heads/"+branch))

	git(t, work, "reset", "--hard", "main")
	git(t, work, "commit", "-q", "--allow-empty", "-m", "stale-lease")
	err := forge.PushBranches(ctx, work, []BranchPush{{Branch: branch, ExpectedSHA: first}})
	require.Error(t, err)
	assert.Equal(t, second, remoteRef(t, origin, "refs/heads/"+branch), "stale lease must not overwrite the remote ref")
}

func TestLocalForgePRMethodsReturnHonestErrors(t *testing.T) {
	forge := NewLocalForge("/tmp/origin.git")
	_, err := forge.ListStackPRs(context.Background(), "o", "r", "loom/")
	require.Error(t, err)
	assert.EqualError(t, err, "local forge has no PR support")
}

func TestPublishLocalOriginPathPushesBranchesOnly(t *testing.T) {
	ctx := context.Background()
	_, origin, work := setupLocalOrigin(t)
	id := sl.StackID("epic:E")
	const ws = "WS"

	store := stackstore.New(t.TempDir())
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: ws, RepoName: "repo", RootBase: "main"}))
	node, err := store.AddNode(ctx, ws, id, "T1", "", sl.CommitModeLoom)
	require.NoError(t, err)

	git(t, work, "checkout", "-q", "-B", node.OutputBranch, "main")
	require.NoError(t, os.WriteFile(filepath.Join(work, "T1.txt"), []byte("work"), 0o644))
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "-m", "T1")
	head := strings.TrimSpace(git(t, work, "rev-parse", node.OutputBranch))

	forge, selected, err := NewForgeForRepo(ctx, work, "")
	require.NoError(t, err)
	require.Equal(t, OriginKindLocal, selected.Kind)
	require.Equal(t, origin, selected.URL)

	rec := &Reconciler{Store: store, Forge: forge}
	report, err := rec.Publish(ctx, ws, id, work, Options{})
	require.NoError(t, err)
	assert.Equal(t, "pushed branches to local origin (no PRs)", report.Message)
	assert.Equal(t, []string{"T1"}, report.Pushed)
	assert.Empty(t, report.Created)
	assert.Empty(t, report.PRURLs)
	assert.Equal(t, head, remoteRef(t, origin, "refs/heads/"+node.OutputBranch))

	nodes, err := store.ListNodes(ctx, ws, id)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, sl.NodeStatePublished, nodes[0].State)
	assert.Equal(t, 0, nodes[0].PRNumber)
	assert.Empty(t, nodes[0].PRURL)
	assert.Equal(t, head, nodes[0].OutputSHA)
}
