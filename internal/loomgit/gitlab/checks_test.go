//go:build gitlab_real

package gitlab

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/errcode"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

func (s *state) goCheck(name string) error {
	switch name {
	case "no_error":
		// Proves the preceding action completed without an error.
		return s.lastErr
	case "default_config_signing_lfs":
		// Proves default Git options use allowlisted user config, signed commits and LFS.
		return s.packageTests("./internal/loomgit/internal/gitexec", "Test(DefaultOptionsReadAllowlistedGlobalConfig|GlobalLFSFilterSurvivesIsolation|GlobalSigningConfigSurvivesIsolation)")
	case "dirty_recovery":
		// Proves incomplete and complete agent recovery preserve untracked work.
		return s.packageTests("./internal/cli/agent", "TestRecoverWorktree_(IncompleteRun_RequeuesTaskAndKeepsUntrackedFiles|CompleteRun_TrustsStatusAndKeepsFiles|ColdWorkspacePreservesUntrackedFile)")
	case "namespace_conflict":
		// Proves the requested user branch blocks Loom's namespace with a typed error.
		err := refname.CheckNamespace(s.dir, "WS")
		if !errors.Is(err, &errcode.Error{Kind: errcode.RefNamespaceConflict}) {
			return fmt.Errorf("namespace error: %v", err)
		}
		return nil
	case "fresh_pr_review":
		// Proves a PR review gets a detached worktree and preserves prior scratch.
		return s.packageTests("./internal/localworkspace", "Test(EnsureDetachedGitWorktreeAtPRHead|EnsureDetachedGitWorktreeAtPRHeadRejectsFastForwardedTip|PRReviewWorktreeAddFailureLeavesNoDirectory)")
	case "journal_crash":
		// Proves a started SQLite entry survives SIGKILL under CGO_ENABLED=0.
		return s.journalCrash()
	case "revision_round_trip":
		// Proves revision hashes and imported trees survive source and derived operations.
		return s.packageTests("./internal/loomgit/internal/changeset", "Test(FreezeSourceStoresRewrittenChainAndReplay|ImportMatchingTreeAndSecondSourceRevision|DerivedOperationsAndNextAttemptKeepOldRevision)")
	case "revision_diff":
		// Real Git fixtures cover committed and Snapshot work, the patch budget, interdiff, and missing base.
		return s.packageTests("./internal/loomgit/gitread", "Test(SnapshotDiffInterdiffBudgetAndMissingBase|DerivedRevisionOffersRangeDiff)")
	case "driver_freeze":
		return s.packageTests("./internal/loomgit/driverfreeze", "TestFreeze(FlatDiffAndNextAttempt|CommittedAndUncommittedWork)")
	case "fresh_task_copy":
		return s.packageTests("./internal/driver", "Test(LocalTaskWorktreeResolverCreatesIsolatedTaskRunWorktree|BridgeRetryUsesDistinctFreezeRequestAndPatchArtifact)")
	case "noncompleted_revision":
		if err := s.packageTests("./internal/cli/agent/tsruntime", "TestFailedLeafFreezesReturnedPatchWithoutChangingHost"); err != nil {
			return err
		}
		return s.packageTests("./internal/driver", "TestHostBridgeRetainsCaptureFailureWithoutFreezingEmptyPatch")
	case "cancelled_revision":
		if err := s.packageTests("./internal/driver", "Test(CancelCapturesRetainedCopyAndKillsCLIChild|CancelMarksIncompleteCaptureAndRetainsCopy|CancelFreezesCommittedTaskCopyWork)"); err != nil {
			return err
		}
		return s.packageTests("./internal/loomgit/internal/changeset", "TestIncompleteCaptureOnlyFreezesAsCancelled")
	case "trial_merge":
		// Proves clean, conflict, dropped-commit, and old-version behavior under the lab Git.
		return s.packageTests("./internal/loomgit/internal/replay", "Test(TrialMergeCleanAndConflictLeaveCheckoutUnchanged|TrialMergeMultiCommitAndDrop|TrialMergeStopsAtFirstConflictingCommit|TrialMergeUsesFirstParentOfMergeCommit|TrialMergePreservesAuthorMessageAndTrailers|VersionRequirement)")
	case "approved_apply":
		return s.packageTests("./internal/loomgit/apply", "TestApply.*")
	case "delegate_from_state":
		for _, check := range []struct{ pkg, tests string }{
			{"./internal/driver", "TestDelegatedTaskFromCurrentWorkspaceState"},
			{"./internal/loomgit/apply", "TestApplyWIPBasedRevisionLeavesUserEditsUncommitted"},
			{"./internal/loomgit/taskcopy", "TestConflictResolutionCopyCreatesNewRevisionForReview"},
		} {
			if err := s.packageTests(check.pkg, check.tests); err != nil {
				return err
			}
		}
		return nil
	case "lead_working_areas":
		if err := s.packageTests("./internal/loomgit/workspace", "TestEnsureWorkingAreaSeparatesLeadsAndKeepsTheirWork"); err != nil {
			return err
		}
		return s.packageTests("./internal/loomgit/apply", "TestAppliedLogInterleavesOwnAndTaskLayers")
	case "publish_only_pr":
		if err := s.packageTests("./internal/loomgit/publish", "TestPublishRecordedRequiresVerdictBeforePush"); err != nil {
			return err
		}
		return s.packageTests("./internal/loomgit/internal/pool", "TestCreateRemoveAndFailedAdd")
	case "review_patch_ids":
		return s.packageTests("./internal/loomgit/review", "TestCarryForwardCleanPatchIDsAndEmptyDroppedCommit")
	case "process_lock":
		// Proves two processes contend on one repository lock and stale leases recover.
		return s.packageTests("./internal/loomgit/internal/pool", "Test(ConcurrentProcesses|StaleLeaseAfterProcessExit)")
	case "capture_after_kill":
		// Proves a killed task process leaves an edit that the exit capture records.
		return s.captureAfterKill()
	case "supervisor_exit_capture":
		// Proves daemon exit and yield paths invoke capture and retain failures.
		return s.packageTests("./internal/cli/daemon/supervisor", "Test(AgentExitCapturesLargeTrackedAndUntrackedWork|AgentExitCapturesAfterDrainRemovedYieldFile|AgentExitCaptureFailureRetainsWork)")
	case "second_repo_rollback":
		// Proves failure adding the second repository rolls back the first checkout.
		return s.packageTests("./internal/cli/serve/workspacemgr", "TestP18SecondWorktreeAddFailureRollsBackFirst")
	case "workspace_creation_crash_adoption":
		// Proves interrupted creation and attachment are adopted or kept for repair.
		return s.packageTests("./internal/cli/serve/workspacemgr", "TestP19.*")
	case "clone_mode":
		for _, check := range []struct{ pkg, tests string }{
			{"./internal/cli/serve/workspacemgr", "TestP120.*"},
			{"./internal/loomgit/workspace", "TestP120CleanupFreshClone.*"},
			{"./internal/loomgit/mirror", "TestP120CloneTaskSnapshotRefsMirrorFromCloneStore"},
		} {
			if err := s.packageTests(check.pkg, check.tests); err != nil {
				return err
			}
		}
		return nil
	case "explicit_commit":
		return s.checkExplicitCommit()
	case "fixture_inventory":
		// Proves the mixed fixture includes nested Git, submodule, LFS, special paths and files.
		return s.checkFixture()
	case "repeat_capture":
		// Proves a second capture advances the same attempt ref without changing HEAD.
		if s.previous.CaptureSHA == "" || s.result.CaptureSHA == "" || s.previous.CaptureSHA == s.result.CaptureSHA {
			return fmt.Errorf("repeat capture did not advance: %s -> %s", s.previous.CaptureSHA, s.result.CaptureSHA)
		}
		ref, err := gitCommand(s.dir, "rev-parse", s.result.CaptureRef)
		if err != nil || ref != s.result.CaptureSHA {
			return fmt.Errorf("capture ref=%s, SHA=%s, err=%v", ref, s.result.CaptureSHA, err)
		}
		return nil
	case "ignored_capture_inventory":
		return s.packageTests("./internal/loomgit/internal/capture", "TestIgnored(NestedRepository|ExtendedAttribute)DoesNotMakeCaptureIncomplete")
	case "provider_policy":
		// Proves the bare remote accepts ordinary pushes and rejects secrets and oversized blobs.
		return s.checkProvider()
	case "mirror_refs":
		return s.packageTests("./internal/loomgit/mirror", "Test.*")
	case "leased_publish":
		return s.packageTests("./internal/loomgit/publish", "TestPublish.*")
	case "landing_detection":
		return s.packageTests("./internal/loomgit/landing", "Test(MergeRecordLandsWithoutTrailerAndOffersDependent|MergedWaitsForFetchedTrunk|RestackOfferSurvivesCallbackFailure|OpenOwnedPRNeverLandsFromCopiedTrailer|SecondaryNeedsOwnedPRAssociation|FetchFailureChangesNoStatus)")
	case "fault_tools":
		// Proves the lab has an ENOSPC tmpfs and a second Unix account.
		return s.checkFaultTools()
	default:
		return fmt.Errorf("unknown Go check %q", name)
	}
}

func (s *state) checkExplicitCommit() error {
	ctx := context.Background()
	store, err := journal.OpenSQLite(filepath.Join(s.t.TempDir(), "settings.sqlite"))
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	for _, name := range []string{"x.rs", "y.rs"} {
		if err := os.WriteFile(filepath.Join(s.dir, name), []byte(name+"\n"), 0600); err != nil {
			return err
		}
	}
	if _, err := gitCommand(s.dir, "add", "y.rs"); err != nil {
		return err
	}
	indexBefore, err := gitCommand(s.dir, "ls-files", "--stage")
	if err != nil {
		return err
	}
	sha, err := loomgit.Commit(ctx, s.dir, store, loomgit.CommitRequest{Workspace: "lab", Paths: []string{"x.rs"}, Message: "agent", ChangeID: "c1", Agent: "codex"})
	if err != nil {
		return err
	}
	indexAfter, err := gitCommand(s.dir, "ls-files", "--stage")
	if err != nil || indexBefore != indexAfter {
		return fmt.Errorf("user index changed: %v", err)
	}
	paths, err := gitCommand(s.dir, "diff-tree", "--no-commit-id", "--name-only", "-r", sha)
	if err != nil || paths != "x.rs" {
		return fmt.Errorf("commit paths=%q: %v", paths, err)
	}
	if _, err := loomgit.Commit(ctx, s.dir, store, loomgit.CommitRequest{Workspace: "lab", Message: "empty", ChangeID: "c2", Agent: "codex"}); err == nil {
		return errors.New("empty path set accepted")
	}
	if _, err := loomgit.Commit(ctx, s.dir, store, loomgit.CommitRequest{Workspace: "lab", Paths: []string{"x.rs"}, Message: "unchanged", ChangeID: "c2", Agent: "codex"}); err == nil {
		return errors.New("empty tree accepted")
	}
	mergePath, err := gitCommand(s.dir, "rev-parse", "--git-path", "MERGE_HEAD")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(mergePath) {
		mergePath = filepath.Join(s.dir, mergePath)
	}
	if err := os.WriteFile(mergePath, []byte(sha+"\n"), 0600); err != nil {
		return err
	}
	if _, err := loomgit.Commit(ctx, s.dir, store, loomgit.CommitRequest{Workspace: "lab", Paths: []string{"x.rs"}, Message: "merge", ChangeID: "c2", Agent: "codex"}); err == nil {
		return errors.New("merge in progress accepted")
	}
	return nil
}

func (s *state) packageTests(pkg, pattern string) error {
	cmd := exec.Command("go", "test", "-count=1", "-run", "^"+pattern+"$", pkg) //nolint:norawexec // Scenario runs named existing real-Git regression tests.
	root, err := filepath.Abs("../../..")
	if err != nil {
		return err
	}
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", strings.Join(cmd.Args, " "), err, out)
	}
	s.t.Logf("%s: %s", pattern, strings.TrimSpace(string(out)))
	return nil
}

func TestLabJournalChild(t *testing.T) {
	if os.Getenv("GITLAB_JOURNAL_CHILD") != "1" {
		return
	}
	store, err := journal.OpenSQLite(os.Getenv("GITLAB_JOURNAL_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Begin(context.Background(), "crash-request", "capture"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	time.Sleep(time.Hour)
}

func (s *state) journalCrash() error {
	path := filepath.Join(s.t.TempDir(), "journal.db")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLabJournalChild$") //nolint:norawexec // Child is killed to test journal durability.
	cmd.Env = append(os.Environ(), "GITLAB_JOURNAL_CHILD=1", "GITLAB_JOURNAL_PATH="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || ready != "READY\n" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("child ready: %q, %v", ready, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		return err
	}
	_ = cmd.Wait()
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer store.Close()
	entries, err := store.OpenEntries(context.Background())
	if err != nil || len(entries) != 1 || entries[0].Phase != "started" {
		return fmt.Errorf("post-kill entries: %+v, %v", entries, err)
	}
	_, err = store.Takeover(context.Background(), entries[0])
	return err
}

func (s *state) captureAfterKill() error {
	cmd := exec.Command("sleep", "30") //nolint:norawexec // Disposable task process is killed as the fault injection.
	if err := cmd.Start(); err != nil {
		return err
	}
	path := filepath.Join(s.dir, "lab-exit.txt")
	if err := os.WriteFile(path, []byte("work before kill\n"), 0644); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	if err := cmd.Process.Kill(); err != nil {
		return err
	}
	if err := cmd.Wait(); err == nil {
		return fmt.Errorf("task process was not killed")
	}
	result, err := agentcapture.Capture(context.Background(), s.dir, "lab", "kill", "P1.4", "capture after kill")
	if err != nil {
		return err
	}
	if result.SHA == "" || !result.Complete {
		return fmt.Errorf("capture after kill: %+v", result)
	}
	_, err = gitCommand(s.dir, "cat-file", "-e", result.SHA+":lab-exit.txt")
	return err
}

func (s *state) checkFixture() error {
	for _, path := range []string{".npmrc", ".env", ".env.local", "id_rsa", "src/id_utils.go", "nested-repo/.git", "vendor/nested/.git", "assets/sample.lfs", "empty-dir"} {
		if _, err := os.Lstat(filepath.Join(s.dir, path)); err != nil {
			return fmt.Errorf("fixture %s: %w", path, err)
		}
	}
	if requireFile(s.t, filepath.Join(s.dir, "readme-link")).Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("readme-link is not a symlink")
	}
	if size := requireFile(s.t, filepath.Join(s.dir, "large-150MiB.bin")).Size(); size != 150<<20 {
		return fmt.Errorf("large file: %d", size)
	}
	if mode := requireFile(s.t, filepath.Join(s.dir, "unreadable.txt")).Mode().Perm(); mode != 0 {
		return fmt.Errorf("unreadable mode: %o", mode)
	}
	pointer, err := gitCommand(s.dir, "show", "HEAD:assets/sample.lfs")
	if err != nil || !strings.HasPrefix(pointer, "version https://git-lfs.github.com/spec/v1") {
		return fmt.Errorf("LFS pointer: %q, %v", pointer, err)
	}
	stage, err := gitCommand(s.dir, "ls-files", "--stage", "vendor/nested")
	if err != nil || !strings.HasPrefix(stage, "160000 ") {
		return fmt.Errorf("submodule stage: %q, %v", stage, err)
	}
	return nil
}

func (s *state) checkProvider() error {
	remote := s.makeProvider()
	if _, err := gitCommand(s.dir, "remote", "add", "provider", remote); err != nil {
		return err
	}
	if _, err := gitCommand(s.dir, "push", "provider", "HEAD:refs/heads/main"); err != nil {
		return err
	}
	if _, err := gitCommand(s.dir, "checkout", "-qb", "secret"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.dir, ".env"), []byte("TOKEN=x"), 0600); err != nil {
		return err
	}
	if _, err := gitCommand(s.dir, "add", ".env"); err != nil {
		return err
	}
	if _, err := gitCommand(s.dir, "commit", "-qm", "secret fixture"); err != nil {
		return err
	}
	if _, err := gitCommand(s.dir, "push", "provider", "HEAD:refs/heads/secret"); err == nil || !strings.Contains(err.Error(), "secret path refused") {
		return fmt.Errorf("secret push: %v", err)
	}
	if _, err := gitCommand(s.dir, "checkout", "-q", "main"); err != nil {
		return err
	}
	if _, err := gitCommand(s.dir, "checkout", "-qb", "oversize"); err != nil {
		return err
	}
	large, err := os.Create(filepath.Join(s.dir, "oversize.bin"))
	if err != nil {
		return err
	}
	if err := large.Truncate(101 << 20); err != nil {
		_ = large.Close()
		return err
	}
	if err := large.Close(); err != nil {
		return err
	}
	if _, err := gitCommand(s.dir, "add", "oversize.bin"); err != nil {
		return err
	}
	if _, err := gitCommand(s.dir, "commit", "-qm", "oversize fixture"); err != nil {
		return err
	}
	if _, err := gitCommand(s.dir, "push", "provider", "HEAD:refs/heads/oversize"); err == nil || !strings.Contains(err.Error(), "exceeds 100 MiB") {
		return fmt.Errorf("oversize push: %v", err)
	}
	return nil
}

func (s *state) checkFaultTools() error {
	if err := os.WriteFile("/small/fill", make([]byte, 2<<20), 0600); !errors.Is(err, syscall.ENOSPC) {
		return fmt.Errorf("small tmpfs: %v", err)
	}
	cmd := exec.Command("su", "-s", "/bin/sh", "labother", "-c", "id -un") //nolint:norawexec // Confirms the second Unix user in the lab image.
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "labother" {
		return fmt.Errorf("second user: %q, %v", out, err)
	}
	return nil
}
