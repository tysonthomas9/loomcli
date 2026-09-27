// Package stack implements the `loom stack` command group: register and inspect
// stack lineage and publish it as stacked PRs. Lineage is loomcli-side (a local
// stackstore); publishing uses the repo-scoped GitHub forge + reconciler.
package stack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

var stackCmd = &cobra.Command{
	Use:     "stack",
	Short:   "Manage stack lineage and publish stacked pull requests",
	GroupID: "workspace",
}

func init() {
	stackCmd.AddCommand(
		initCmd(), listCmd(), showCmd(), statusCmd(), validateCmd(),
		addCmd(), moveCmd(), setBaseCmd(), removeCmd(), publishCmd(), mergeCmd(), restackCmd(),
	)
	cli.RegisterCommand(stackCmd)
}

// helpers --------------------------------------------------------------------

// activeWorkspace returns the workspace key from LOOM_WORKSPACE (which the root
// command mirrors --workspace into). Stack lineage is local, so this avoids
// opening the fleet-db store for simple edits.
func activeWorkspace() (string, error) {
	ws := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE"))
	if ws == "" {
		return "", errors.New("LOOM_WORKSPACE is required (set it or pass --workspace)")
	}
	return ws, nil
}

func openStore() (*stackstore.LocalStore, error) { return stackstore.Default() }

var shaRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

func resolveGitHubToken(ctx context.Context) string {
	if t := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); t != "" {
		return t
	}
	if t := strings.TrimSpace(os.Getenv("GH_TOKEN")); t != "" {
		return t
	}
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

// resolveRepoPath resolves the local checkout for a stack's repo and fails closed
// with a clear, actionable error rather than letting a stale path surface later
// as a generic git failure (per the proposal's "validate the registered local
// repo path" / fail-closed stale-path requirement).
func resolveRepoPath(ws, repoName, override string) (string, error) {
	p := strings.TrimSpace(override)
	if p == "" {
		sc, err := bootstrap.LoadStateCache()
		if err != nil {
			return "", err
		}
		local, ok := sc.Workspaces[ws]
		if !ok {
			return "", fmt.Errorf("workspace %q has no local state on this machine; run `loom workspace use %s` or pass --repo-path", ws, ws)
		}
		p = strings.TrimSpace(localworkspace.RepoPath(local, repoName))
		if p == "" {
			return "", fmt.Errorf("repo %q is not checked out in workspace %q; pass --repo-path", repoName, ws)
		}
	}
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("repo path %q is missing or not a directory (stale local state?)", p)
	}
	if _, err := os.Stat(filepath.Join(p, ".git")); err != nil {
		return "", fmt.Errorf("repo path %q is not a git checkout (stale local state?)", p)
	}
	return p, nil
}

// commands -------------------------------------------------------------------

func initCmd() *cobra.Command {
	var repo, base, mode string
	var jsonOut bool
	c := &cobra.Command{
		Use:   "init <stack-id>",
		Short: "Create a stack (e.g. epic:EPIC-1) rooted on a base branch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := activeWorkspace()
			if err != nil {
				return err
			}
			if strings.TrimSpace(base) == "" {
				return errors.New("--base is required (a branch name)")
			}
			if shaRe.MatchString(base) {
				return fmt.Errorf("--base must be a branch name, not a commit SHA: %q", base)
			}
			if strings.TrimSpace(repo) == "" {
				return errors.New("--repo is required")
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			stack := sl.Stack{
				ID: sl.StackID(args[0]), WorkspaceKey: ws, RepoName: repo,
				RootBase: base, DefaultCommitMode: sl.CommitMode(mode),
			}
			if err := st.EnsureStack(cmd.Context(), stack); err != nil {
				return err
			}
			if jsonOut {
				return cmdstore.WriteJSON(stack)
			}
			fmt.Printf("stack %s ready (repo=%s base=%s)\n", stack.ID, repo, base)
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", "", "workspace repo name")
	c.Flags().StringVar(&base, "base", "", "root base branch (e.g. main)")
	c.Flags().StringVar(&mode, "commit-mode", "", "default commit mode: loom_commit|agent_commit|squash_on_publish")
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return c
}

func listCmd() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:   "list",
		Short: "List stacks in the active workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := activeWorkspace()
			if err != nil {
				return err
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			stacks, err := st.ListStacks(cmd.Context(), ws)
			if err != nil {
				return err
			}
			if jsonOut {
				return cmdstore.WriteJSON(stacks)
			}
			if len(stacks) == 0 {
				fmt.Println("no stacks")
				return nil
			}
			for _, s := range stacks {
				fmt.Printf("%s  repo=%s base=%s\n", s.ID, s.RepoName, s.RootBase)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return c
}

func showCmd() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:   "show <stack-id>",
		Short: "Show a stack and its ordered units",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, st, id, err := loadCtx(args[0])
			if err != nil {
				return err
			}
			stack, err := st.GetStack(cmd.Context(), ws, id)
			if err != nil {
				return err
			}
			nodes, err := st.ListNodes(cmd.Context(), ws, id)
			if err != nil {
				return err
			}
			if jsonOut {
				return cmdstore.WriteJSON(map[string]any{"stack": stack, "nodes": nodes})
			}
			fmt.Printf("%s  repo=%s base=%s\n", stack.ID, stack.RepoName, stack.RootBase)
			for _, n := range nodes {
				fmt.Printf("  %-16s base=%-16s branch=%s state=%s\n",
					n.TaskID, baseOrRoot(n, stack.RootBase), n.OutputBranch, n.State)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return c
}

//nolint:funlen // Cobra command construction includes live status enrichment and output formatting together.
func statusCmd() *cobra.Command {
	var jsonOut bool
	var repoPath string
	c := &cobra.Command{
		Use:   "status <stack-id>",
		Short: "Show each unit's PR, state, and live health (checks/review/mergeable)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, st, id, err := loadCtx(args[0])
			if err != nil {
				return err
			}
			stack, err := st.GetStack(cmd.Context(), ws, id)
			if err != nil {
				return err
			}
			// Enrich with live PR health when a repo checkout + token resolve.
			path, _ := resolveRepoPath(ws, stack.RepoName, repoPath)
			token := resolveGitHubToken(cmd.Context())
			rp := ""
			if path != "" && token != "" {
				rp = path
			}
			rec := &stackpublish.Reconciler{Store: st, Forge: stackpublish.NewGitHubForge(token, nil, "")}
			report, err := rec.StackStatus(cmd.Context(), ws, id, rp)
			if err != nil {
				return err
			}
			if jsonOut {
				return cmdstore.WriteJSON(report)
			}
			for _, row := range report.Rows {
				pr := "-"
				if row.PRNumber > 0 {
					pr = fmt.Sprintf("#%d", row.PRNumber)
				}
				next := ""
				if row.NextToMerge {
					next = "  <- next to merge"
				}
				if report.Live {
					fmt.Printf("  %-16s %-10s %-5s [ci:%s review:%s merge:%s] %s%s\n",
						row.TaskID, row.State, pr, dash(row.Checks), dash(row.Review), dash(row.Mergeable), row.PRURL, next)
				} else {
					fmt.Printf("  %-16s %-10s %-5s %s%s\n", row.TaskID, row.State, pr, row.PRURL, next)
				}
			}
			if !report.Live {
				fmt.Println("(local view; pass --repo-path or set GITHUB_TOKEN for live PR health)")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	c.Flags().StringVar(&repoPath, "repo-path", "", "local checkout (enables live PR health)")
	return c
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func validateCmd() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:   "validate <stack-id>",
		Short: "Check the stack's lineage is linear and acyclic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, st, id, err := loadCtx(args[0])
			if err != nil {
				return err
			}
			nodes, err := st.ListNodes(cmd.Context(), ws, id)
			if err != nil {
				return err
			}
			_, oerr := sl.Ordered(nodes)
			if jsonOut {
				res := map[string]any{"ok": oerr == nil}
				if oerr != nil {
					res["error"] = oerr.Error()
				}
				return cmdstore.WriteJSON(res)
			}
			if oerr != nil {
				return fmt.Errorf("invalid: %w", oerr)
			}
			fmt.Printf("ok: %d unit(s), valid lineage\n", len(nodes))
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return c
}

func addCmd() *cobra.Command {
	var stackID, after, mode string
	var jsonOut, root bool
	c := &cobra.Command{
		Use:   "add <task-id> --stack <stack-id>",
		Short: "Register a task in a stack (appends to the tip; --after to chain, --root for a parallel chain)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, st, id, err := loadCtxFlag(stackID)
			if err != nil {
				return err
			}
			if root && after != "" {
				return errors.New("--root and --after are mutually exclusive")
			}
			base := after
			// --root starts a new parallel chain off the stack base. Otherwise, with
			// no --after, append to the current tip (the tail of the last chain).
			if !root && base == "" {
				nodes, lerr := st.ListNodes(cmd.Context(), ws, id)
				if lerr != nil {
					return lerr
				}
				if ordered, oerr := sl.Ordered(nodes); oerr == nil && len(ordered) > 0 {
					base = ordered[len(ordered)-1].TaskID
				}
			}
			node, err := st.AddNode(cmd.Context(), ws, id, args[0], base, sl.CommitMode(mode))
			if err != nil {
				return err
			}
			if jsonOut {
				return cmdstore.WriteJSON(node)
			}
			fmt.Printf("added %s (base=%s branch=%s)\n", node.TaskID, baseOrRoot(node, "<root>"), node.OutputBranch)
			return nil
		},
	}
	c.Flags().StringVar(&stackID, "stack", "", "stack id (required)")
	c.Flags().StringVar(&after, "after", "", "predecessor task id (default: current tip)")
	c.Flags().BoolVar(&root, "root", false, "start a new parallel chain off the stack base")
	c.Flags().StringVar(&mode, "commit-mode", "", "commit mode override")
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	_ = c.MarkFlagRequired("stack")
	return c
}

func moveCmd() *cobra.Command {
	var stackID, after string
	c := &cobra.Command{
		Use:   "move <task-id> --stack <stack-id> --after <task-id>",
		Short: "Reorder a unit to sit after another unit",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, st, id, err := loadCtxFlag(stackID)
			if err != nil {
				return err
			}
			if after == "" {
				return errors.New("--after is required")
			}
			if err := st.MoveNode(cmd.Context(), ws, id, args[0], after); err != nil {
				return err
			}
			fmt.Printf("moved %s after %s\n", args[0], after)
			return nil
		},
	}
	c.Flags().StringVar(&stackID, "stack", "", "stack id (required)")
	c.Flags().StringVar(&after, "after", "", "task id to move after (required)")
	_ = c.MarkFlagRequired("stack")
	return c
}

func setBaseCmd() *cobra.Command {
	var stackID, baseTask string
	c := &cobra.Command{
		Use:   "set-base <task-id> --stack <stack-id> --base-task <task-id>",
		Short: "Set a unit's predecessor (\"\" for root)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, st, id, err := loadCtxFlag(stackID)
			if err != nil {
				return err
			}
			if err := st.SetBase(cmd.Context(), ws, id, args[0], baseTask); err != nil {
				return err
			}
			fmt.Printf("set base of %s to %q\n", args[0], baseTask)
			return nil
		},
	}
	c.Flags().StringVar(&stackID, "stack", "", "stack id (required)")
	c.Flags().StringVar(&baseTask, "base-task", "", "predecessor task id (empty = root)")
	_ = c.MarkFlagRequired("stack")
	return c
}

func removeCmd() *cobra.Command {
	var stackID string
	c := &cobra.Command{
		Use:   "remove <task-id> --stack <stack-id>",
		Short: "Remove a unit (children reparent onto its predecessor)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, st, id, err := loadCtxFlag(stackID)
			if err != nil {
				return err
			}
			if err := st.RemoveNode(cmd.Context(), ws, id, args[0]); err != nil {
				return err
			}
			fmt.Printf("removed %s\n", args[0])
			return nil
		},
	}
	c.Flags().StringVar(&stackID, "stack", "", "stack id (required)")
	_ = c.MarkFlagRequired("stack")
	return c
}

func restackCmd() *cobra.Command {
	var repoPath string
	var headless, jsonOut bool
	c := &cobra.Command{
		Use:   "restack <stack-id>",
		Short: "Rebase descendants of merged units onto the live base, resolving conflicts with an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, st, id, err := loadCtx(args[0])
			if err != nil {
				return err
			}
			stack, err := st.GetStack(cmd.Context(), ws, id)
			if err != nil {
				return err
			}
			path, err := resolveRepoPath(ws, stack.RepoName, repoPath)
			if err != nil {
				return err
			}
			token := resolveGitHubToken(cmd.Context())
			if token == "" {
				return errors.New("no GitHub token (set GITHUB_TOKEN/GH_TOKEN or run `gh auth login`)")
			}
			rec := &stackpublish.Reconciler{Store: st, Forge: stackpublish.NewGitHubForge(token, nil, "")}
			report, err := rec.Restack(cmd.Context(), ws, id, path, newResolver(headless))
			if err != nil {
				return err
			}
			if jsonOut {
				return cmdstore.WriteJSON(report)
			}
			fmt.Printf("restacked: rebased=%d resolved=%d\n", len(report.Rebased), len(report.Resolved))
			return nil
		},
	}
	c.Flags().StringVar(&repoPath, "repo-path", "", "local checkout to rebase in (default: from loom state)")
	c.Flags().BoolVar(&headless, "headless", false, "resolve conflicts with a non-interactive (headless) agent")
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return c
}

//nolint:funlen // Cobra command construction includes option wiring, publish execution, and output formatting.
func publishCmd() *cobra.Command {
	var repoPath string
	var dryRun, jsonOut, autoRebase, headless bool
	c := &cobra.Command{
		Use:   "publish <stack-id>",
		Short: "Publish the stack as stacked pull requests",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, st, id, err := loadCtx(args[0])
			if err != nil {
				return err
			}
			stack, err := st.GetStack(cmd.Context(), ws, id)
			if err != nil {
				return err
			}
			path, err := resolveRepoPath(ws, stack.RepoName, repoPath)
			if err != nil {
				return err
			}
			token := resolveGitHubToken(cmd.Context())
			if token == "" && !dryRun {
				return errors.New("no GitHub token (set GITHUB_TOKEN/GH_TOKEN or run `gh auth login`)")
			}
			rec := &stackpublish.Reconciler{
				Store: st,
				Forge: stackpublish.NewGitHubForge(token, nil, ""),
			}
			opts := stackpublish.Options{DryRun: dryRun}
			// Seed PR titles/bodies from issue metadata when available; the
			// reconciler falls back to the owned commit's subject otherwise.
			opts.PRMetaFor = func(ctx context.Context, taskID string) (stackpublish.PRMeta, bool) {
				d, derr := cli.DefaultIssueBackend().Get(ctx, taskID)
				if derr != nil || d == nil {
					return stackpublish.PRMeta{}, false
				}
				return stackpublish.PRMeta{
					Title:              d.Title,
					Summary:            d.Description,
					AcceptanceCriteria: d.AcceptanceCriteria,
				}, true
			}
			if autoRebase {
				opts.Resolver = newResolver(headless)
			}
			report, err := rec.Publish(cmd.Context(), ws, id, path, opts)
			if err != nil {
				return err
			}
			if jsonOut {
				return cmdstore.WriteJSON(report)
			}
			verb := "published"
			if dryRun {
				verb = "plan"
			}
			fmt.Printf("%s: created=%d reparented=%d skipped=%d closed=%d merged=%d empty=%d\n",
				verb, len(report.Created), len(report.Reparented), len(report.Skipped),
				len(report.Closed), len(report.Merged), len(report.Empty))
			for task, url := range report.PRURLs {
				fmt.Printf("  %s  %s\n", task, url)
			}
			return nil
		},
	}
	c.Flags().StringVar(&repoPath, "repo-path", "", "local checkout to push from (default: from loom state)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "compute the plan without mutating GitHub")
	c.Flags().BoolVar(&autoRebase, "auto-rebase", false, "rebase descendants of merged units onto the live base (agent resolves conflicts) instead of failing closed")
	c.Flags().BoolVar(&headless, "headless", false, "with --auto-rebase, resolve conflicts headlessly (non-interactive)")
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return c
}

func mergeCmd() *cobra.Command {
	var repoPath, matchHead, authorEmail, subject, body, bodyFile string
	var jsonOut, useMerge, useSquash, useRebase, auto, disableAuto, admin, deleteBranch bool
	c := &cobra.Command{
		Use:   "merge <stack-id> [<pr-number>|<url>|<branch>|<task-id>]",
		Short: "Merge the stack's next-to-merge PR and reconcile the stack",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, st, id, err := loadCtx(args[0])
			if err != nil {
				return err
			}
			stack, err := st.GetStack(cmd.Context(), ws, id)
			if err != nil {
				return err
			}
			path, err := resolveRepoPath(ws, stack.RepoName, repoPath)
			if err != nil {
				return err
			}
			token := resolveGitHubToken(cmd.Context())
			if token == "" {
				return errors.New("no GitHub token (set GITHUB_TOKEN/GH_TOKEN or run `gh auth login`)")
			}
			opts, err := mergeOptionsFromFlags(cmd, useMerge, useSquash, useRebase, auto, disableAuto, admin, deleteBranch, matchHead, authorEmail, subject, body, bodyFile)
			if err != nil {
				return err
			}
			target := ""
			if len(args) == 2 {
				target = args[1]
			}
			rec := &stackpublish.Reconciler{
				Store: st,
				Forge: stackpublish.NewGitHubForge(token, nil, ""),
			}
			report, err := rec.MergeNext(cmd.Context(), ws, id, path, target, opts)
			if err != nil {
				return err
			}
			if jsonOut {
				return cmdstore.WriteJSON(report)
			}
			fmt.Printf("merged: #%d %s (%s)\n", report.MergedPR.Number, report.MergedPR.URL, report.MergedPR.TaskID)
			if len(report.NextToMerge) == 0 {
				fmt.Println("nextToMerge: none")
				return nil
			}
			fmt.Println("nextToMerge:")
			for _, row := range report.NextToMerge {
				pr := "-"
				if row.PRNumber > 0 {
					pr = fmt.Sprintf("#%d", row.PRNumber)
				}
				fmt.Printf("  %-16s %-5s %s\n", row.TaskID, pr, row.OutputBranch)
			}
			return nil
		},
	}
	c.Flags().StringVar(&repoPath, "repo-path", "", "local checkout to reconcile after merge (default: from loom state)")
	c.Flags().BoolVarP(&useMerge, "merge", "m", false, "merge the commits with the base branch")
	c.Flags().BoolVarP(&useSquash, "squash", "s", false, "squash the commits into one commit and merge it")
	c.Flags().BoolVarP(&useRebase, "rebase", "r", false, "rebase the commits onto the base branch")
	c.Flags().BoolVar(&auto, "auto", false, "automatically merge after necessary requirements are met")
	c.Flags().BoolVar(&disableAuto, "disable-auto", false, "disable auto-merge for this pull request")
	c.Flags().BoolVar(&admin, "admin", false, "use administrator privileges to merge a pull request that does not meet requirements")
	c.Flags().StringVar(&matchHead, "match-head-commit", "", "commit SHA that the pull request head must match to allow merge")
	c.Flags().StringVarP(&authorEmail, "author-email", "A", "", "email text for merge commit author")
	c.Flags().StringVarP(&subject, "subject", "t", "", "subject text for the merge commit")
	c.Flags().StringVarP(&body, "body", "b", "", "body text for the merge commit")
	c.Flags().StringVarP(&bodyFile, "body-file", "F", "", "read body text from file (use \"-\" to read from standard input)")
	c.Flags().BoolVarP(&deleteBranch, "delete-branch", "d", false, "delete the local and remote branch after merge")
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	c.MarkFlagsMutuallyExclusive("merge", "squash", "rebase")
	c.MarkFlagsMutuallyExclusive("auto", "disable-auto")
	c.MarkFlagsMutuallyExclusive("body", "body-file")
	return c
}

func mergeOptionsFromFlags(cmd *cobra.Command, useMerge, useSquash, useRebase, auto, disableAuto, admin, deleteBranch bool, matchHead, authorEmail, subject, body, bodyFile string) (stackpublish.MergeOptions, error) {
	opts := stackpublish.MergeOptions{
		Auto: auto, DisableAuto: disableAuto, Admin: admin, DeleteBranch: deleteBranch,
		MatchHeadCommit: strings.TrimSpace(matchHead),
		AuthorEmail:     strings.TrimSpace(authorEmail),
	}
	switch {
	case useMerge:
		opts.Method = stackpublish.MergeMethodMerge
	case useSquash:
		opts.Method = stackpublish.MergeMethodSquash
	case useRebase:
		opts.Method = stackpublish.MergeMethodRebase
	}
	if cmd.Flags().Changed("subject") {
		opts.Subject = subject
		opts.SubjectSet = true
	}
	if cmd.Flags().Changed("body") {
		opts.Body = body
		opts.BodySet = true
	}
	if strings.TrimSpace(bodyFile) != "" {
		data, err := readMergeBodyFile(bodyFile)
		if err != nil {
			return stackpublish.MergeOptions{}, err
		}
		opts.Body = string(data)
		opts.BodySet = true
	}
	return opts, nil
}

func readMergeBodyFile(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	data, err := os.ReadFile(path) //nolint:gosec // explicit CLI-provided file path, matching gh's body-file behavior
	if err != nil {
		return nil, fmt.Errorf("read --body-file %q: %w", path, err)
	}
	return data, nil
}

// shared loaders -------------------------------------------------------------

func loadCtx(stackID string) (string, *stackstore.LocalStore, sl.StackID, error) {
	ws, err := activeWorkspace()
	if err != nil {
		return "", nil, "", err
	}
	st, err := openStore()
	if err != nil {
		return "", nil, "", err
	}
	return ws, st, sl.StackID(stackID), nil
}

func loadCtxFlag(stackID string) (string, *stackstore.LocalStore, sl.StackID, error) {
	if strings.TrimSpace(stackID) == "" {
		return "", nil, "", errors.New("--stack is required")
	}
	return loadCtx(stackID)
}

func baseOrRoot(n sl.Node, root string) string {
	if n.BaseTaskID == "" {
		return root
	}
	return n.BaseTaskID
}
