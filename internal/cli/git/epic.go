package git

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
)

// `loom epic` productionizes the flue epic-branch sync flow: create a shared
// integration branch for an epic, run its tasks onto it (LOOM_FLUE_SYNC=
// epic-branch), then open one PR from the epic branch to base. It reuses the
// same git/gh plumbing as `loom pr`.

var (
	epicWorktree      string
	epicBase          string
	epicRemote        string
	epicRequireClosed bool
)

var epicCmd = &cobra.Command{
	Use:     "epic",
	Short:   "Manage epic integration branches and PRs (flue epic-branch sync)",
	GroupID: "git",
	Long: `Create a shared "epic branch" for an epic's tasks and open one PR for it.

Each task in the epic commits onto the epic branch (run them with
LOOM_FLUE_SYNC=epic-branch LOOM_FLUE_EPIC_BRANCH=<branch>), so the epic's work
accumulates on one integration branch; then 'loom epic pr' opens a single PR
from that branch to base.

Examples:
  loom epic start PROJ-42                 # create loom/epic-PROJ-42 off base
  loom epic pr PROJ-42                    # open the PR for the epic branch
  loom epic pr PROJ-42 --require-closed   # only if all the epic's tasks are closed`,
}

var epicStartCmd = &cobra.Command{
	Use:   "start <epic-id>",
	Short: "Create the epic's integration branch (loom/epic-<id>) off the base branch",
	Args:  cobra.ExactArgs(1),
	RunE:  runEpicStart,
}

var epicPRCmd = &cobra.Command{
	Use:   "pr <epic-id>",
	Short: "Open (or report) the PR from the epic branch to the base branch",
	Args:  cobra.ExactArgs(1),
	RunE:  runEpicPR,
}

func init() {
	epicCmd.PersistentFlags().StringVar(&epicWorktree, "worktree", "", "Repo worktree/dir to operate in (default: current directory)")
	epicCmd.PersistentFlags().StringVar(&epicBase, "base", "", "Base branch (default: the remote's default branch)")
	epicCmd.PersistentFlags().StringVar(&epicRemote, "remote", "origin", "Git remote")
	epicPRCmd.Flags().BoolVar(&epicRequireClosed, "require-closed", false, "Only open the PR if all the epic's tasks are closed")
	epicCmd.AddCommand(epicStartCmd, epicPRCmd)
	cli.RegisterCommand(epicCmd)
}

// epicBranchName is the integration branch for an epic. Must match the runner's
// expectation (LOOM_FLUE_EPIC_BRANCH); the id is sanitised to a safe ref.
func epicBranchName(epicID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r == '.' || r == '_' || r == '-':
			return r
		default:
			return '-'
		}
	}, epicID)
	return "loom/epic-" + safe
}

func epicRepoPath() string {
	if epicWorktree != "" {
		return epicWorktree
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

// resolveEpicBase returns the base branch: the --base flag, else the remote's
// default branch (so master vs main is auto-detected), else "main".
func resolveEpicBase(deps *cli.Deps, repoPath, remote string) string {
	if epicBase != "" {
		return epicBase
	}
	r := resolveRemote(remote)
	res := deps.Exec.Run(repoPath, "git", "ls-remote", "--symref", r, "HEAD")
	if res.Err == nil {
		for _, line := range strings.Split(res.Stdout, "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) >= 2 && fields[0] == "ref:" {
				return strings.TrimPrefix(fields[1], "refs/heads/")
			}
		}
	}
	return "main"
}

func runEpicStart(cmd *cobra.Command, args []string) error {
	deps := cli.GetDeps(cmd)
	epicID := args[0]
	repoPath := epicRepoPath()
	branch := epicBranchName(epicID)
	base := resolveEpicBase(deps, repoPath, epicRemote)

	created, err := createRemoteBranchFromBase(deps, repoPath, epicRemote, base, branch)
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("✓ created epic branch %s off %s\n", branch, base)
	} else {
		fmt.Printf("✓ epic branch %s already exists (off %s)\n", branch, base)
	}
	fmt.Println("\nRun this epic's tasks onto it with:")
	fmt.Printf("  LOOM_FLUE_SANDBOX=daytona LOOM_FLUE_SYNC=epic-branch LOOM_FLUE_EPIC_BRANCH=%s \\\n", branch)
	fmt.Println("    loom task <agent> --auto --backend flue")
	fmt.Printf("\nThen open the PR with:  loom epic pr %s\n", epicID)
	return nil
}

// createRemoteBranchFromBase creates <branch> on the remote at the tip of
// <base>, without touching the local worktree. Idempotent: a no-op if the
// branch already exists on the remote.
func createRemoteBranchFromBase(deps *cli.Deps, repoPath, remote, base, branch string) (created bool, err error) {
	r := resolveRemote(remote)
	if err := validateGitRef(branch); err != nil {
		return false, err
	}
	if err := validateGitRef(base); err != nil {
		return false, err
	}
	if err := gitFetchRemote(deps, repoPath, remote); err != nil {
		return false, fmt.Errorf("fetching %s: %v", r, err)
	}
	if ls := deps.Exec.Run(repoPath, "git", "ls-remote", "--heads", r, "refs/heads/"+branch); ls.Err == nil && strings.TrimSpace(ls.Stdout) != "" {
		return false, nil // already exists
	}
	// Push the remote base tip to the new branch ref (refs/remotes/<r>/<base>
	// exists after the fetch); nothing is checked out locally.
	src := fmt.Sprintf("refs/remotes/%s/%s", r, base)
	push := deps.Exec.Run(repoPath, "git", "push", r, src+":refs/heads/"+branch)
	if push.Err != nil {
		return false, fmt.Errorf("creating %s off %s: %s", branch, base, strings.TrimSpace(push.Stderr+push.Stdout))
	}
	return true, nil
}

func runEpicPR(cmd *cobra.Command, args []string) error {
	deps := cli.GetDeps(cmd)
	if err := checkGhInstalled(deps); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	epicID := args[0]
	repoPath := epicRepoPath()
	branch := epicBranchName(epicID)
	base := resolveEpicBase(deps, repoPath, epicRemote)

	title, body, openTasks := epicPRContent(epicID)
	if epicRequireClosed && len(openTasks) > 0 {
		return fmt.Errorf("epic %s still has %d open task(s): %s\n(drop --require-closed to PR anyway)",
			epicID, len(openTasks), strings.Join(openTasks, ", "))
	}

	res, err := createPRFromRemoteBranch(deps, repoPath, branch, base, epicRemote, title, body)
	if err != nil {
		return err
	}
	switch {
	case res.NoCommits:
		fmt.Printf("No commits on %s beyond %s — has the epic's work been pushed? Nothing to PR.\n", branch, base)
	case res.AlreadyExists:
		fmt.Printf("✓ PR already open: %s\n", res.URL)
	default:
		fmt.Printf("✓ opened PR: %s\n", res.URL)
	}
	return nil
}

// createPRFromRemoteBranch opens a PR for a branch that ALREADY exists on the
// remote (the epic branch the runners pushed to) — unlike CreatePRResult it does
// not push a local branch. title/body are explicit.
func createPRFromRemoteBranch(deps *cli.Deps, repoPath, source, target, remote, title, body string) (*PRResult, error) {
	if err := validateGitRef(source); err != nil {
		return nil, err
	}
	if err := validateGitRef(target); err != nil {
		return nil, err
	}
	if err := gitFetchRemote(deps, repoPath, remote); err != nil {
		return nil, fmt.Errorf("fetching: %v", err)
	}
	if hasCommits, err := hasCommitsBetweenRemoteDeps(deps, repoPath, remote, target, source); err == nil && !hasCommits {
		return &PRResult{NoCommits: true}, nil
	}
	result := deps.Exec.Run(repoPath, "gh", "pr", "create",
		"--base", target, "--head", source, "--title", title, "--body", body)
	if result.Err != nil {
		errMsg := result.Stderr + result.Stdout
		if strings.Contains(errMsg, "already exists") {
			url, urlErr := getExistingPRURL(deps, repoPath, source)
			if urlErr != nil {
				return nil, urlErr
			}
			return &PRResult{URL: url, AlreadyExists: true}, nil
		}
		return nil, fmt.Errorf("creating PR: %s", strings.TrimSpace(errMsg))
	}
	return &PRResult{URL: strings.TrimSpace(result.Stdout), Created: true}, nil
}

// epicPRContent builds the PR title/body from the epic + its child tasks
// (best-effort — falls back to the id if the issue backend is unreachable), and
// returns the ids of any still-open tasks.
func epicPRContent(epicID string) (title, body string, openTasks []string) {
	title = "Epic " + epicID
	ctx, cancel := cmdstore.SignalContext()
	defer cancel()
	ib := cli.DefaultIssueBackend()

	var b strings.Builder
	if detail, err := ib.Get(ctx, epicID); err == nil && detail != nil && detail.Title != "" {
		title = fmt.Sprintf("Epic: %s (%s)", detail.Title, epicID)
		b.WriteString(detail.Title + "\n\n")
	}
	if children, err := ib.GetChildren(ctx, epicID); err == nil && len(children) > 0 {
		b.WriteString("Tasks:\n")
		for _, c := range children {
			done := isClosedStatus(c.Status)
			mark := " "
			if done {
				mark = "x"
			} else {
				openTasks = append(openTasks, c.ID)
			}
			b.WriteString(fmt.Sprintf("- [%s] %s — %s (%s)\n", mark, c.ID, c.Title, c.Status))
		}
	}
	b.WriteString("\n— opened with `loom epic pr`")
	return title, b.String(), openTasks
}

func isClosedStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "closed", "done", "tombstone":
		return true
	default:
		return false
	}
}
