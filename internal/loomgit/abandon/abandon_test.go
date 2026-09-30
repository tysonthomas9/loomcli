package abandon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskcopy"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:norawexec
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type fakeForge struct {
	closeError error
	closed     int
	comment    string
}

func (forge *fakeForge) ClosePR(_ context.Context, _, _ string, number int, comment string) error {
	forge.closed++
	forge.comment = comment
	if number != 7 {
		return errors.New("wrong PR")
	}
	return forge.closeError
}

type fakePusher struct {
	sha     string
	deleted int
}

type fakeClaims struct {
	backend.IssueBackend
	releases int
	task     string
	actor    string
	holder   string
}

func (claims *fakeClaims) CurrentIssueLockHolder(context.Context, string) (string, error) {
	if claims.holder != "" {
		return claims.holder, nil
	}
	return "task-copy-42", nil
}

type activeSessions struct{ store.AgentSessionStore }

func (activeSessions) List(context.Context, string, store.AgentSessionFilter) ([]*domain.AgentSession, error) {
	return []*domain.AgentSession{{Kind: domain.AgentSessionKindTask, Status: domain.AgentSessionRunning}}, nil
}

func TestAbandonRefusesLiveTaskBeforeCapture(t *testing.T) {
	service := &Service{JournalPath: filepath.Join(t.TempDir(), "store.db"), Claims: &fakeClaims{}, Sessions: activeSessions{}}
	err := service.ensureNoLiveTask(context.Background(), "W", "T")
	if err == nil || !strings.Contains(err.Error(), "stop it before abandoning") {
		t.Fatalf("live task guard = %v", err)
	}
	journalStore, err := journal.OpenSQLite(service.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = journalStore.Close() }()
	claims := service.Claims.(*fakeClaims)
	err = service.finish(context.Background(), journalStore, journal.Abandonment{Workspace: "W", Task: "T", ClaimActor: "agent"})
	if err == nil || claims.releases != 0 {
		t.Fatalf("live task release = %d, %v", claims.releases, err)
	}
}

func TestAbandonRefusesChangedClaimHolder(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "store.db")
	journalStore, err := journal.OpenSQLite(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = journalStore.Close() }()
	claims := &fakeClaims{holder: "another-task-copy"}
	service := &Service{JournalPath: journalPath, Claims: claims, Sessions: memstore.New().AgentSessions()}
	err = service.releaseClaim(context.Background(), journalStore, journal.Abandonment{
		Workspace: "W", Change: "C", Task: "T", ClaimActor: "original-task-copy",
	})
	if err == nil || !strings.Contains(err.Error(), "holder changed") || claims.releases != 0 {
		t.Fatalf("changed-holder release = %d, %v", claims.releases, err)
	}
}

func (claims *fakeClaims) ReleaseIssueLock(_ context.Context, task, actor string) error {
	claims.releases++
	claims.task, claims.actor = task, actor
	return nil
}

func (pusher *fakePusher) RemoteSHA(context.Context, string, string) (string, error) {
	return pusher.sha, nil
}

func (pusher *fakePusher) Push(_ context.Context, _, _, local, expected string) error {
	if local != "" || expected != pusher.sha {
		return errors.New("delete lacked exact lease")
	}
	pusher.deleted++
	pusher.sha = ""
	return nil
}

func TestAbandonCapturesKeepsIgnoredAndRetriesPRClose(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", root)
	t.Setenv("HOME", root)
	source, copyPath := filepath.Join(root, "source"), filepath.Join(root, "task")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, source, "init", "-q")
	git(t, source, "config", "user.name", "Test")
	git(t, source, "config", "user.email", "test@example.test")
	if err := os.WriteFile(filepath.Join(source, ".gitignore"), []byte(".env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, source, "add", ".gitignore")
	git(t, source, "commit", "-qm", "base")
	base := git(t, source, "rev-parse", "HEAD")
	journalPath := filepath.Join(root, "loomgit", "store.db")
	if _, err := taskcopy.CreateDetailedAt(ctx, journalPath, source, copyPath, "W", "A", "", base); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "work.txt"), []byte("retained work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyPath, ".env"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	change, err := store.DriverChange(ctx, "W", "T", "source", "change")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLocalLineage(ctx, journal.LocalLineage{Workspace: "W", Task: "dependent", Repo: "source",
		PredecessorChange: change, PredecessorRevision: 1, BaseSHA: base}); err != nil {
		t.Fatal(err)
	}
	branch, err := refname.ChangeBranch("W", change)
	if err != nil {
		t.Fatal(err)
	}
	git(t, source, "branch", branch, base)
	git(t, source, "remote", "add", "origin", filepath.Join(root, "remote.git"))
	if err := store.BeginPublication(ctx, journal.Publication{Workspace: "W", Change: change, Repo: source,
		Branch: branch, Trunk: "main", Slug: "owner/repo", Head: base}); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvancePublication(ctx, journal.Publication{Workspace: "W", Change: change, Repo: source,
		Branch: branch, Trunk: "main", Slug: "owner/repo", Head: base, PRNumber: 7, Phase: "done"}); err != nil {
		t.Fatal(err)
	}
	forge := &fakeForge{closeError: errors.New("forge unavailable")}
	pusher := &fakePusher{sha: base}
	claims := &fakeClaims{}
	service := &Service{JournalPath: journalPath, Forge: forge, Pusher: pusher, Claims: claims, Sessions: memstore.New().AgentSessions()}
	request := Request{Workspace: "W", Task: "T", Repo: "source", Attempt: "A", Worktree: copyPath,
		SourceRepo: source, Reason: "superseded", RequestedBy: "operator"}
	preview, err := service.Run(ctx, request)
	var confirmation *ConfirmationRequired
	if !errors.As(err, &confirmation) || len(preview.Ignored) != 1 || preview.Ignored[0].Path != ".env" || preview.Ignored[0].Size != 7 {
		t.Fatalf("ignored preview = %+v, %v", preview, err)
	}
	request.ConfirmIgnored = true
	result, err := service.Run(ctx, request)
	if err == nil || !strings.Contains(err.Error(), "forge unavailable") {
		t.Fatalf("expected close failure, got %v", err)
	}
	if !result.Complete || result.Revision.Outcome != "abandoned" || !result.RetentionEligible || len(result.Dependents) != 1 {
		t.Fatalf("abandon result = %+v", result)
	}
	if claims.releases != 1 || claims.task != "T" || claims.actor != "task-copy-42" {
		t.Fatalf("claim release = %+v", claims)
	}
	retention, exists, err := store.Abandonment(ctx, "W", change)
	if err != nil || !exists || !retention.RetentionEligible || retention.Worktree != copyPath || retention.SourceRepo != source {
		t.Fatalf("retention record = %+v, %t, %v", retention, exists, err)
	}
	if _, err := os.Stat(filepath.Join(copyPath, ".env")); err != nil {
		t.Fatalf("ignored file was removed: %v", err)
	}
	if got := git(t, source, "show", result.Revision.HeadSHA+":work.txt"); got != "retained work" {
		t.Fatalf("revision omitted work: %q", got)
	}
	if abandoned, err := store.ChangeAbandoned(ctx, "W", change); err != nil || !abandoned {
		t.Fatalf("change abandoned = %t, %v", abandoned, err)
	}
	lineage, err := taskcopy.ReadLineageStatus(ctx, "W", "dependent", "source")
	if err != nil || lineage.State != "dependency_abandoned" {
		t.Fatalf("dependent lineage = %+v, %v", lineage, err)
	}
	if pusher.deleted != 1 {
		t.Fatalf("remote branch deletion calls = %d", pusher.deleted)
	}
	if exists, err := gitexec.RefExists(source, "refs/heads/"+branch); err != nil || !exists {
		t.Fatalf("local change branch was removed: %t, %v", exists, err)
	}
	forge.closeError = nil
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if forge.closed != 2 || !strings.Contains(forge.comment, "superseded") || pusher.deleted != 1 || claims.releases != 1 {
		t.Fatalf("reconcile close=%d comment=%q deletes=%d", forge.closed, forge.comment, pusher.deleted)
	}
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if forge.closed != 2 {
		t.Fatal("completed PR close replayed")
	}
}

func TestIncompleteAbandonKeepsCopyAndDoesNotTouchPR(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", root)
	t.Setenv("HOME", root)
	source, copyPath := filepath.Join(root, "source"), filepath.Join(root, "task")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, source, "init", "-q")
	git(t, source, "config", "user.name", "Test")
	git(t, source, "config", "user.email", "test@example.test")
	if err := os.WriteFile(filepath.Join(source, "base"), []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, source, "add", "base")
	git(t, source, "commit", "-qm", "base")
	base := git(t, source, "rev-parse", "HEAD")
	journalPath := filepath.Join(root, "loomgit", "store.db")
	if _, err := taskcopy.CreateDetailedAt(ctx, journalPath, source, copyPath, "W", "A", "", base); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "id_ed25519"), []byte("not captured"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	change, err := store.DriverChange(ctx, "W", "T", "source", "change")
	if err != nil {
		t.Fatal(err)
	}
	branch, err := refname.ChangeBranch("W", change)
	if err != nil {
		t.Fatal(err)
	}
	git(t, source, "remote", "add", "origin", filepath.Join(root, "remote.git"))
	if err := store.BeginPublication(ctx, journal.Publication{Workspace: "W", Change: change, Repo: source,
		Branch: branch, Trunk: "main", Slug: "owner/repo", Head: base}); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvancePublication(ctx, journal.Publication{Workspace: "W", Change: change, Repo: source,
		Branch: branch, Trunk: "main", Slug: "owner/repo", Head: base, PRNumber: 7, Phase: "done"}); err != nil {
		t.Fatal(err)
	}
	forge := &fakeForge{}
	pusher := &fakePusher{sha: base}
	claims := &fakeClaims{}
	service := &Service{JournalPath: journalPath, Forge: forge, Pusher: pusher, Claims: claims, Sessions: memstore.New().AgentSessions()}
	result, err := service.Run(ctx, Request{Workspace: "W", Task: "T", Repo: "source", Attempt: "A",
		Worktree: copyPath, SourceRepo: source, Reason: "incomplete", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Complete || result.RetentionEligible || !result.Revision.Incomplete || forge.closed != 0 || pusher.deleted != 0 || claims.releases != 1 {
		t.Fatalf("incomplete abandonment = %+v; close=%d delete=%d", result, forge.closed, pusher.deleted)
	}
	if _, err := os.Stat(filepath.Join(copyPath, "id_ed25519")); err != nil {
		t.Fatalf("task copy was removed: %v", err)
	}
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if forge.closed != 0 || pusher.deleted != 0 {
		t.Fatal("reconcile ran unrequested PR actions")
	}
	repeated, err := service.Run(ctx, Request{Workspace: "W", Task: "T", Repo: "source", Attempt: "A",
		Worktree: copyPath, SourceRepo: source, Reason: "incomplete", RequestedBy: "operator", ClosePR: true, DeleteRemote: true})
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Complete || forge.closed != 1 || pusher.deleted != 1 {
		t.Fatalf("explicit incomplete actions = %+v; close=%d delete=%d", repeated, forge.closed, pusher.deleted)
	}
	if _, err := os.Stat(filepath.Join(copyPath, "id_ed25519")); err != nil {
		t.Fatalf("explicit PR actions removed task copy: %v", err)
	}
}

func TestReconcileCompletesAbandonedDependentMarker(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.BeginAbandonment(ctx, journal.Abandonment{Workspace: "W", Change: "C", Task: "T",
		ClaimActor: "task-copy-42", RequestedBy: "operator", Reason: "superseded"}); err != nil {
		t.Fatal(err)
	}
	claims := &fakeClaims{}
	if err := (&Service{JournalPath: path, Claims: claims, Sessions: memstore.New().AgentSessions()}).Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if claims.releases != 1 || claims.task != "T" || claims.actor != "task-copy-42" {
		t.Fatalf("reconciled claim release = %+v", claims)
	}
	abandoned, err := store.ChangeAbandoned(ctx, "W", "C")
	if err != nil || !abandoned {
		t.Fatalf("reconciled abandonment = %t, %v", abandoned, err)
	}
}

func TestHostPusherDeletesBranchWithExactLease(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source, remote := filepath.Join(root, "source"), filepath.Join(root, "remote.git")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, source, "init", "-q")
	git(t, source, "config", "user.name", "Test")
	git(t, source, "config", "user.email", "test@example.test")
	git(t, source, "commit", "--allow-empty", "-qm", "base")
	git(t, root, "init", "--bare", "-q", remote)
	ref := "refs/heads/loom/ws/W/change/C"
	git(t, source, "push", remote, "HEAD:"+ref)
	runner, err := gitexec.New(source, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Test", Email: "test@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	pusher := hostPusher{runner: runner}
	head, err := pusher.RemoteSHA(ctx, remote, ref)
	if err != nil || head == "" {
		t.Fatalf("remote head = %q, %v", head, err)
	}
	if err := pusher.Push(ctx, remote, ref, "", strings.Repeat("0", len(head))); err == nil {
		t.Fatal("stale lease deleted branch")
	}
	if err := pusher.Push(ctx, remote, ref, "", head); err != nil {
		t.Fatal(err)
	}
	actual, err := pusher.RemoteSHA(ctx, remote, ref)
	if err != nil || actual != "" {
		t.Fatalf("deleted branch = %q, %v", actual, err)
	}
}
