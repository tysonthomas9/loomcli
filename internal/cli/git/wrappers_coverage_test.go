package git

import (
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func installWrapperCoverageDeps(t *testing.T) (*cli.Deps, *MockGitRunner, *MockExecRunner) {
	t.Helper()
	deps, gitRunner, execRunner, _, _ := NewTestDeps(t)

	oldGitDefault := defaultDeps
	defaultDeps = deps

	cliDefault := cli.TestingGetDefaultDeps()
	oldCLIGit := cliDefault.Git
	oldCLIExec := cliDefault.Exec
	oldCLILookPath := cliDefault.LookPath
	cliDefault.Git = deps.Git
	cliDefault.Exec = deps.Exec
	cliDefault.LookPath = deps.LookPath

	t.Cleanup(func() {
		defaultDeps = oldGitDefault
		cliDefault.Git = oldCLIGit
		cliDefault.Exec = oldCLIExec
		cliDefault.LookPath = oldCLILookPath
	})
	return deps, gitRunner, execRunner
}

func TestExportedGitWrappersUseDefaultDeps(t *testing.T) {
	_, gitRunner, _ := installWrapperCoverageDeps(t)
	stashListCalls := 0
	gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
		joined := strings.Join(args, " ")
		switch {
		case joined == "status --porcelain":
			return cli.CommandResult{Stdout: ""}
		case joined == "diff --name-only --diff-filter=U":
			return cli.CommandResult{Stdout: "conflict.txt\n"}
		case strings.HasPrefix(joined, "log "):
			return cli.CommandResult{Stdout: "abc123 commit\n"}
		case joined == "worktree list --porcelain":
			return cli.CommandResult{Stdout: "worktree /repo\nbranch refs/heads/feature\n\n"}
		case joined == "remote get-url origin":
			return cli.CommandResult{Stdout: "git@example.test/repo.git\n"}
		case joined == "stash list":
			stashListCalls++
			if stashListCalls >= 2 {
				return cli.CommandResult{Stdout: "stash@{0}: WIP\n"}
			}
			return cli.CommandResult{Stdout: ""}
		case joined == "rev-parse --verify refs/heads/feature":
			return cli.CommandResult{}
		case joined == "rev-parse --verify refs/remotes/origin/feature":
			return cli.CommandResult{}
		case joined == "clean -fdn":
			return cli.CommandResult{Stdout: "Would remove tmp.txt\n"}
		case joined == "clean -fdn --exclude=.loom/**":
			return cli.CommandResult{Stdout: "Would remove tmp.txt\n"}
		case joined == "merge --abort":
			return cli.CommandResult{}
		default:
			return cli.CommandResult{Stdout: "ok\n"}
		}
	}

	repo := "/repo"
	if out, err := RunGitCommand(repo, "status", "--porcelain"); err != nil || out != "" {
		t.Fatalf("RunGitCommand out=%q err=%v", out, err)
	}
	if err := RunGitCommandWithOutput(repo, "status"); err != nil {
		t.Fatalf("RunGitCommandWithOutput: %v", err)
	}
	if err := GitFetch(repo); err != nil {
		t.Fatalf("GitFetch: %v", err)
	}
	if err := GitCheckout(repo, "feature"); err != nil {
		t.Fatalf("GitCheckout: %v", err)
	}
	if err := GitPull(repo, "main"); err != nil {
		t.Fatalf("GitPull: %v", err)
	}
	if err := GitMerge(repo, "feature", "merge msg"); err != nil {
		t.Fatalf("GitMerge: %v", err)
	}
	if err := GitMergeOrigin(repo, "main", "merge msg"); err != nil {
		t.Fatalf("GitMergeOrigin: %v", err)
	}
	if err := GitPush(repo, "feature"); err != nil {
		t.Fatalf("GitPush: %v", err)
	}
	if err := GitPushForce(repo, "feature"); err != nil {
		t.Fatalf("GitPushForce: %v", err)
	}
	if err := GitReset(repo, "HEAD"); err != nil {
		t.Fatalf("GitReset: %v", err)
	}
	if err := GitClean(repo); err != nil {
		t.Fatalf("GitClean: %v", err)
	}
	if out, err := GitCleanDryRun(repo); err != nil || !strings.Contains(out, "tmp.txt") {
		t.Fatalf("GitCleanDryRun out=%q err=%v", out, err)
	}
	if err := GitCleanExclude(repo, []string{".loom/**"}); err != nil {
		t.Fatalf("GitCleanExclude: %v", err)
	}
	if out, err := GitCleanDryRunExclude(repo, []string{".loom/**"}); err != nil || !strings.Contains(out, "tmp.txt") {
		t.Fatalf("GitCleanDryRunExclude out=%q err=%v", out, err)
	}
	if files, err := GetConflictedFiles(repo); err != nil || len(files) != 1 || files[0] != "conflict.txt" {
		t.Fatalf("GetConflictedFiles files=%v err=%v", files, err)
	}
	if ok, err := HasCommitsBetween(repo, "main", "feature"); err != nil || !ok {
		t.Fatalf("HasCommitsBetween ok=%t err=%v", ok, err)
	}
	if clean, err := IsCleanWorkingTree(repo); err != nil || !clean {
		t.Fatalf("IsCleanWorkingTree clean=%t err=%v", clean, err)
	}
	if err := GitFetchRemote(repo, "origin"); err != nil {
		t.Fatalf("GitFetchRemote: %v", err)
	}
	if err := GitMergeRemote(repo, "origin", "main", "merge msg"); err != nil {
		t.Fatalf("GitMergeRemote: %v", err)
	}
	if err := GitPushRemote(repo, "origin", "feature"); err != nil {
		t.Fatalf("GitPushRemote: %v", err)
	}
	if err := GitPullRemote(repo, "origin", "main"); err != nil {
		t.Fatalf("GitPullRemote: %v", err)
	}
	if ok, err := HasCommitsBetweenRemote(repo, "origin", "main", "feature"); err != nil || !ok {
		t.Fatalf("HasCommitsBetweenRemote ok=%t err=%v", ok, err)
	}
	if checkedOut, path, err := IsRefCheckedOutInWorktree(repo, "feature"); err != nil || !checkedOut || path != "/repo" {
		t.Fatalf("IsRefCheckedOutInWorktree checkedOut=%t path=%q err=%v", checkedOut, path, err)
	}
	if err := GitCheckoutDetached(repo, "origin/main"); err != nil {
		t.Fatalf("GitCheckoutDetached: %v", err)
	}
	if err := GitCreateBranchFromHead(repo, "tmp-branch"); err != nil {
		t.Fatalf("GitCreateBranchFromHead: %v", err)
	}
	if err := GitDeleteBranch(repo, "tmp-branch", true); err != nil {
		t.Fatalf("GitDeleteBranch: %v", err)
	}
	if err := GitPushRefspec(repo, "origin", "tmp-branch", "main"); err != nil {
		t.Fatalf("GitPushRefspec: %v", err)
	}
	if stashed, err := GitStash(repo); err != nil || !stashed {
		t.Fatalf("GitStash stashed=%t err=%v", stashed, err)
	}
	if err := GitStashPop(repo); err != nil {
		t.Fatalf("GitStashPop: %v", err)
	}
	if ok, err := BranchExistsLocally(repo, "feature"); err != nil || !ok {
		t.Fatalf("BranchExistsLocally ok=%t err=%v", ok, err)
	}
	if ok, err := RemoteBranchExists(repo, "origin", "feature"); err != nil || !ok {
		t.Fatalf("RemoteBranchExists ok=%t err=%v", ok, err)
	}
	if err := GitCheckoutNewFromRef(repo, "new-branch", "origin/main"); err != nil {
		t.Fatalf("GitCheckoutNewFromRef: %v", err)
	}
	if err := GitMergeAbort(repo); err != nil {
		t.Fatalf("GitMergeAbort: %v", err)
	}
	if ok, err := HasUnmergedFiles(repo); err != nil || !ok {
		t.Fatalf("HasUnmergedFiles ok=%t err=%v", ok, err)
	}
	if count, err := getStashCount(repo); err != nil || count != 1 {
		t.Fatalf("getStashCount count=%d err=%v", count, err)
	}
	if len(gitRunner.RunCalls) == 0 {
		t.Fatal("expected git runner calls")
	}
}

func TestGitAPIResultHelpers(t *testing.T) {
	_, gitRunner, execRunner := installWrapperCoverageDeps(t)
	gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
		switch strings.Join(args, " ") {
		case "log origin/main..feature --oneline":
			return cli.CommandResult{Stdout: ""}
		case "log origin/main..origin/feature --format=%s --reverse":
			return cli.CommandResult{Stdout: "Add feature\n"}
		case "log origin/main..feature --format=%s --reverse":
			return cli.CommandResult{Stdout: "Add feature\n"}
		default:
			return cli.CommandResult{Stdout: "ok\n"}
		}
	}
	execRunner.RunFunc = func(_, name string, args ...string) cli.CommandResult {
		if name == "gh" && strings.Join(args, " ") == "pr create --base main --head feature --title Add feature --body ---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)" {
			return cli.CommandResult{Stdout: "https://github.example.test/pr/1\n"}
		}
		return cli.CommandResult{Stdout: "https://github.example.test/pr/1\n"}
	}

	upToDate, result := checkAlreadyUpToDate("/repo", "origin", "main", "feature")
	if !upToDate || result == nil || !result.AlreadyUpToDate {
		t.Fatalf("checkAlreadyUpToDate = %t, %+v", upToDate, result)
	}
	conflictResult, err := mergeResultToConflicts([]string{"a.txt"}, errors.New("merge failed"))
	if err != nil || conflictResult == nil || conflictResult.Success {
		t.Fatalf("mergeResultToConflicts conflict=%+v err=%v", conflictResult, err)
	}
	rawErr := errors.New("merge failed")
	if result, err := mergeResultToConflicts(nil, rawErr); result != nil || !errors.Is(err, rawErr) {
		t.Fatalf("mergeResultToConflicts no conflicts result=%+v err=%v", result, err)
	}

	pullResult, err := PullRepoWorktreeResult("/repo", "feature", "main", "origin")
	if err != nil || pullResult == nil || !pullResult.Success {
		t.Fatalf("PullRepoWorktreeResult result=%+v err=%v", pullResult, err)
	}
	noCommits, err := CreatePRResult("/repo", "feature", "main", "origin")
	if err != nil || noCommits == nil || !noCommits.NoCommits {
		t.Fatalf("CreatePRResult no commits result=%+v err=%v", noCommits, err)
	}

	locked := (&LockedError{AgentName: "planner", PID: 123, Duration: 0}).Error()
	if !strings.Contains(locked, "planner") || !strings.Contains(locked, "PID 123") {
		t.Fatalf("LockedError = %q", locked)
	}
}

func TestGoGitDiffErrorAndParsingBranches(t *testing.T) {
	_, gitRunner, _ := installWrapperCoverageDeps(t)
	gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
		switch strings.Join(args, " ") {
		case "log main..HEAD --format=%H|%h|%an|%ae|%aI|%s":
			return cli.CommandResult{Stdout: "malformed\nabc|a|ann|ann@example.test|2026-01-02T03:04:05Z|subject\n"}
		case "log bad..HEAD --format=%H|%h|%an|%ae|%aI|%s":
			return cli.CommandResult{Err: errors.New("git log failed")}
		default:
			return cli.CommandResult{Stdout: ""}
		}
	}

	commits, err := DiffCommits(t.Context(), "/repo", "main", 0)
	if err != nil {
		t.Fatalf("DiffCommits parse branch: %v", err)
	}
	if len(commits) != 1 || commits[0].Hash != "abc" {
		t.Fatalf("commits = %+v", commits)
	}
	if _, err := DiffCommits(t.Context(), "/repo", "../bad", 0); err == nil {
		t.Fatal("DiffCommits invalid ref err = nil")
	}
	if _, err := DiffCommits(t.Context(), "/repo", "bad", 0); err == nil || !strings.Contains(err.Error(), "listing commits") {
		t.Fatalf("DiffCommits git error = %v", err)
	}
	if _, err := DiffFilePatch(t.Context(), "/repo", "HEAD", "HEAD", ""); err == nil {
		t.Fatal("DiffFilePatch empty path err = nil")
	}
	if _, err := DiffFiles(t.Context(), "/repo", "../bad", "HEAD"); err == nil {
		t.Fatal("DiffFiles invalid from ref err = nil")
	}
	if _, err := DiffFiles(t.Context(), "/repo", "HEAD", "../bad"); err == nil {
		t.Fatal("DiffFiles invalid to ref err = nil")
	}
	if _, err := DiffFiles(t.Context(), t.TempDir(), "HEAD", "HEAD"); err == nil || !strings.Contains(err.Error(), "open repository") {
		t.Fatalf("DiffFiles non-repo err = %v", err)
	}
	if got := parseNumstatRenamePath("plain.txt"); got != "plain.txt" {
		t.Fatalf("parseNumstatRenamePath plain = %q", got)
	}
}

func TestPullRepoWorktreeResultErrorBranches(t *testing.T) {
	t.Run("conflicts returned after abort", func(t *testing.T) {
		deps, _, _ := installWrapperCoverageDeps(t)
		cmdMock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"diff", "--name-only", "--diff-filter=U"},
				Stdout: "a.txt\nb.txt\n",
			},
			{
				Name: "git",
				Args: []string{"merge", "--abort"},
			},
		})
		cmdMock.InstallOn(deps)
		outputMock := NewOutputCommandMock(t, []OutputCommandStub{
			{Args: []string{"fetch", "origin"}},
			{
				Args: []string{
					"merge", "origin/main", "-m",
					"Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>",
				},
				Err: errors.New("merge conflict"),
			},
		})
		outputMock.InstallOn(deps)

		result, err := PullRepoWorktreeResult("/repo", "feature", "main", "origin")
		if err != nil {
			t.Fatalf("PullRepoWorktreeResult conflicts: %v", err)
		}
		if result == nil || result.Success || len(result.ConflictedFiles) != 2 {
			t.Fatalf("result = %+v, want conflict result with files", result)
		}
	})

	t.Run("merge failure without conflict markers", func(t *testing.T) {
		deps, _, _ := installWrapperCoverageDeps(t)
		cmdMock := NewCommandMock(t, []CommandStub{
			{
				Name: "git",
				Args: []string{"diff", "--name-only", "--diff-filter=U"},
			},
			{
				Name: "git",
				Args: []string{"merge", "--abort"},
			},
		})
		cmdMock.InstallOn(deps)
		outputMock := NewOutputCommandMock(t, []OutputCommandStub{
			{Args: []string{"fetch", "origin"}},
			{
				Args: []string{
					"merge", "origin/main", "-m",
					"Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>",
				},
				Err: errors.New("merge refused"),
			},
		})
		outputMock.InstallOn(deps)

		result, err := PullRepoWorktreeResult("/repo", "feature", "main", "origin")
		if err == nil || !strings.Contains(err.Error(), "merge failed") {
			t.Fatalf("result=%+v err=%v, want merge failure", result, err)
		}
	})

	t.Run("push failure", func(t *testing.T) {
		deps, _, _ := installWrapperCoverageDeps(t)
		cmdMock := NewCommandMock(t, []CommandStub{})
		cmdMock.InstallOn(deps)
		outputMock := NewOutputCommandMock(t, []OutputCommandStub{
			{Args: []string{"fetch", "upstream"}},
			{
				Args: []string{
					"merge", "upstream/trunk", "-m",
					"Pull from trunk\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>",
				},
			},
			{Args: []string{"push", "upstream", "feature"}, Err: errors.New("rejected")},
		})
		outputMock.InstallOn(deps)

		result, err := PullRepoWorktreeResult("/repo", "feature", "trunk", "upstream")
		if err == nil || !strings.Contains(err.Error(), "pushing") {
			t.Fatalf("result=%+v err=%v, want push failure", result, err)
		}
	})
}

func TestGitStatusSummaryAndResetResultHelpers(t *testing.T) {
	_, gitRunner, _ := installWrapperCoverageDeps(t)
	gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
		switch strings.Join(args, " ") {
		case "branch --show-current":
			return cli.CommandResult{Stdout: "feature\n"}
		case "status --porcelain":
			return cli.CommandResult{Stdout: " M changed.txt\n?? new.txt\n"}
		case "diff --name-only --diff-filter=U":
			return cli.CommandResult{Stdout: "conflict.txt\n"}
		case "rev-list --left-right --count feature...origin/main":
			return cli.CommandResult{Stdout: "2\t3\n"}
		case "stash list":
			return cli.CommandResult{Stdout: "stash@{0}: WIP\nstash@{1}: WIP\n"}
		default:
			return cli.CommandResult{Stdout: "ok\n"}
		}
	}

	if branch, err := GetCurrentBranch("/repo"); err != nil || branch != "feature" {
		t.Fatalf("GetCurrentBranch = %q, %v; want feature", branch, err)
	}
	files, err := getChangedFiles("/repo")
	if err != nil {
		t.Fatalf("getChangedFiles: %v", err)
	}
	if len(files) != 2 || files[0] != "changed.txt" || files[1] != "new.txt" {
		t.Fatalf("changed files = %#v, want parsed porcelain files", files)
	}
	ahead, behind := getAheadBehind("/repo", "feature", "main")
	if ahead != 2 || behind != 3 {
		t.Fatalf("ahead/behind = %d/%d, want 2/3", ahead, behind)
	}
	if ahead, behind := getAheadBehind("/repo", "(detached)", "main"); ahead != 0 || behind != 0 {
		t.Fatalf("detached ahead/behind = %d/%d, want 0/0", ahead, behind)
	}

	summary, err := GetGitStatusSummary("/repo", "main")
	if err != nil {
		t.Fatalf("GetGitStatusSummary: %v", err)
	}
	if summary.Branch != "feature" || summary.TargetBranch != "main" || summary.IsClean ||
		summary.Ahead != 2 || summary.Behind != 3 || !summary.HasConflicts || summary.StashCount != 2 {
		t.Fatalf("summary = %+v, want populated dirty/conflicted status", summary)
	}
}

func TestResetWorktreeResultSuccessAndProtectedBranch(t *testing.T) {
	_, gitRunner, _ := installWrapperCoverageDeps(t)
	gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
		switch strings.Join(args, " ") {
		case "branch --show-current":
			return cli.CommandResult{Stdout: "feature\n"}
		default:
			return cli.CommandResult{}
		}
	}

	res, err := ResetWorktreeResult(t.TempDir(), "nova", "main", false, true)
	if err != nil {
		t.Fatalf("ResetWorktreeResult: %v", err)
	}
	if !res.Success || !res.Pushed || res.PreviousBranch != "feature" || !strings.Contains(res.Message, "origin/main") {
		t.Fatalf("reset result = %+v, want pushed success from feature to main", res)
	}

	gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
		if strings.Join(args, " ") == "branch --show-current" {
			return cli.CommandResult{Stdout: "main\n"}
		}
		return cli.CommandResult{}
	}
	if _, err := ResetWorktreeResult(t.TempDir(), "nova", "main", false, true); err == nil || !strings.Contains(err.Error(), "protected branch") {
		t.Fatalf("protected reset error = %v, want protected branch refusal", err)
	}
}

func TestResetWorktreeResultLocked(t *testing.T) {
	installWrapperCoverageDeps(t)
	worktreePath := t.TempDir()
	if err := cli.AcquireLock(worktreePath, "loom task", "worker"); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	t.Cleanup(func() { _ = cli.ReleaseLock(worktreePath) })

	_, err := ResetWorktreeResult(worktreePath, "nova", "main", false, false)
	if err == nil {
		t.Fatal("ResetWorktreeResult returned nil error for locked worktree")
	}
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("error = %T %v, want LockedError", err, err)
	}
	if locked.AgentName != "worker" {
		t.Fatalf("locked agent = %q, want worker", locked.AgentName)
	}
}

func TestCreatePRResultBranches(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		_, gitRunner, execRunner := installWrapperCoverageDeps(t)
		gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
			switch strings.Join(args, " ") {
			case "log origin/main..feature --oneline":
				return cli.CommandResult{Stdout: "abc123 Add feature\n"}
			case "log origin/main..origin/feature --format=%s --reverse":
				return cli.CommandResult{Stdout: "Add feature\nRefine feature\n"}
			default:
				return cli.CommandResult{}
			}
		}
		execRunner.RunFunc = func(_, name string, args ...string) cli.CommandResult {
			if name != "gh" || strings.Join(args[:3], " ") != "pr create --base" {
				t.Fatalf("unexpected exec call: %s %v", name, args)
			}
			return cli.CommandResult{Stdout: "https://github.example.test/pr/2\n"}
		}

		result, err := CreatePRResult("/repo", "feature", "main", "origin")
		if err != nil {
			t.Fatalf("CreatePRResult: %v", err)
		}
		if result == nil || !result.Created || result.URL != "https://github.example.test/pr/2" {
			t.Fatalf("result = %+v, want created PR URL", result)
		}
	})

	t.Run("already exists resolves URL", func(t *testing.T) {
		_, gitRunner, execRunner := installWrapperCoverageDeps(t)
		gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
			switch strings.Join(args, " ") {
			case "log upstream/main..feature --oneline":
				return cli.CommandResult{Stdout: "abc123 Add feature\n"}
			case "log upstream/main..upstream/feature --format=%s --reverse":
				return cli.CommandResult{Stdout: "Add feature\n"}
			default:
				return cli.CommandResult{}
			}
		}
		execRunner.RunFunc = func(_, name string, args ...string) cli.CommandResult {
			if name != "gh" {
				t.Fatalf("unexpected exec call: %s %v", name, args)
			}
			joined := strings.Join(args, " ")
			if strings.HasPrefix(joined, "pr create ") {
				return cli.CommandResult{Stderr: "a pull request for feature already exists", Err: errors.New("exists")}
			}
			if joined == "pr view feature --json url -q .url" {
				return cli.CommandResult{Stdout: "https://github.example.test/pr/existing\n"}
			}
			t.Fatalf("unexpected gh args: %v", args)
			return cli.CommandResult{}
		}

		result, err := CreatePRResult("/repo", "feature", "main", "upstream")
		if err != nil {
			t.Fatalf("CreatePRResult existing: %v", err)
		}
		if result == nil || !result.AlreadyExists || result.URL != "https://github.example.test/pr/existing" {
			t.Fatalf("result = %+v, want existing PR URL", result)
		}
	})

	t.Run("already exists URL lookup fails", func(t *testing.T) {
		_, gitRunner, execRunner := installWrapperCoverageDeps(t)
		gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
			switch strings.Join(args, " ") {
			case "log origin/main..feature --oneline":
				return cli.CommandResult{Stdout: "abc123 Add feature\n"}
			case "log origin/main..origin/feature --format=%s --reverse":
				return cli.CommandResult{Stdout: "Add feature\n"}
			default:
				return cli.CommandResult{}
			}
		}
		execRunner.RunFunc = func(_, _ string, args ...string) cli.CommandResult {
			joined := strings.Join(args, " ")
			if strings.HasPrefix(joined, "pr create ") {
				return cli.CommandResult{Stderr: "already exists", Err: errors.New("exists")}
			}
			return cli.CommandResult{Stderr: "not found", Err: errors.New("not found")}
		}

		if _, err := CreatePRResult("/repo", "feature", "main", "origin"); err == nil || !strings.Contains(err.Error(), "getting existing PR URL") {
			t.Fatalf("error = %v, want existing PR URL lookup failure", err)
		}
	})

	t.Run("create fails", func(t *testing.T) {
		_, gitRunner, execRunner := installWrapperCoverageDeps(t)
		gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
			switch strings.Join(args, " ") {
			case "log origin/main..feature --oneline":
				return cli.CommandResult{Stdout: "abc123 Add feature\n"}
			case "log origin/main..origin/feature --format=%s --reverse":
				return cli.CommandResult{Stdout: "Add feature\n"}
			default:
				return cli.CommandResult{}
			}
		}
		execRunner.RunFunc = func(_, _ string, _ ...string) cli.CommandResult {
			return cli.CommandResult{Stderr: "authentication required\n", Err: errors.New("auth")}
		}

		if _, err := CreatePRResult("/repo", "feature", "main", "origin"); err == nil || !strings.Contains(err.Error(), "authentication required") {
			t.Fatalf("error = %v, want gh create failure", err)
		}
	})

	t.Run("invalid ref", func(t *testing.T) {
		installWrapperCoverageDeps(t)
		if _, err := CreatePRResult("/repo", "feature;bad", "main", "origin"); err == nil {
			t.Fatal("expected invalid ref error")
		}
	})
}

func TestPushBranchInRepoResultSuccessAndErrors(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		_, gitRunner, _ := installWrapperCoverageDeps(t)
		stashListCalls := 0
		gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
			switch strings.Join(args, " ") {
			case "stash list":
				stashListCalls++
				if stashListCalls == 1 {
					return cli.CommandResult{}
				}
				return cli.CommandResult{Stdout: "stash@{0}: WIP\n"}
			case "branch --show-current":
				return cli.CommandResult{Stdout: "feature\n"}
			case "log origin/main..feature --oneline":
				return cli.CommandResult{Stdout: "abc123 Add feature\n"}
			default:
				return cli.CommandResult{}
			}
		}

		result, err := PushBranchInRepoResult("/repo", "feature", "main", "origin")
		if err != nil {
			t.Fatalf("PushBranchInRepoResult: %v", err)
		}
		if result == nil || !result.Success || !strings.Contains(result.Message, "origin/main") {
			t.Fatalf("result = %+v, want successful push", result)
		}
	})

	t.Run("fetch error", func(t *testing.T) {
		_, gitRunner, _ := installWrapperCoverageDeps(t)
		gitRunner.WithOutput = errors.New("network down")
		if _, err := PushBranchInRepoResult("/repo", "feature", "main", "origin"); err == nil || !strings.Contains(err.Error(), "fetching") {
			t.Fatalf("error = %v, want fetch failure", err)
		}
	})

	t.Run("stash error", func(t *testing.T) {
		_, gitRunner, _ := installWrapperCoverageDeps(t)
		gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
			if strings.Join(args, " ") == "stash list" {
				return cli.CommandResult{Err: errors.New("stash list failed")}
			}
			return cli.CommandResult{}
		}
		if _, err := PushBranchInRepoResult("/repo", "feature", "main", "origin"); err == nil || !strings.Contains(err.Error(), "stashing changes") {
			t.Fatalf("error = %v, want stash failure", err)
		}
	})
}

func TestPushBranchInRepoDetachedResultSuccess(t *testing.T) {
	_, gitRunner, _ := installWrapperCoverageDeps(t)
	gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
		switch strings.Join(args, " ") {
		case "log upstream/main..feature --oneline":
			return cli.CommandResult{Stdout: "abc123 Add feature\n"}
		default:
			return cli.CommandResult{}
		}
	}

	result, err := pushBranchInRepoDetachedResult("/repo", "feature", "main", "upstream")
	if err != nil {
		t.Fatalf("pushBranchInRepoDetachedResult: %v", err)
	}
	if result == nil || !result.Success || !strings.Contains(result.Message, "upstream/main") {
		t.Fatalf("result = %+v, want successful detached push", result)
	}
}

func TestCheckGhInstalledWrapper(t *testing.T) {
	_, _, execRunner := installWrapperCoverageDeps(t)
	execRunner.RunFunc = func(_, name string, args ...string) cli.CommandResult {
		if name != "gh" || strings.Join(args, " ") != "--version" {
			t.Fatalf("unexpected exec call: %s %v", name, args)
		}
		return cli.CommandResult{}
	}
	if err := CheckGhInstalled(); err != nil {
		t.Fatalf("CheckGhInstalled: %v", err)
	}

	execRunner.RunFunc = func(_, _ string, _ ...string) cli.CommandResult {
		return cli.CommandResult{Err: errors.New("missing")}
	}
	if err := CheckGhInstalled(); err == nil || !strings.Contains(err.Error(), "gh' CLI not found") {
		t.Fatalf("error = %v, want missing gh message", err)
	}
}
