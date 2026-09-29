package git

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// confirmationSession keeps one buffered reader for the whole command. A new
// reader per prompt can consume answers intended for later workspaces.
type confirmationSession struct {
	reader      *bufio.Reader
	interactive bool
	yes         bool
}

func newConfirmationSession(yes bool) *confirmationSession {
	return &confirmationSession{reader: bufio.NewReader(os.Stdin), interactive: term.IsTerminal(int(os.Stdin.Fd())), yes: yes}
}

func (s *confirmationSession) requireInteractive(command string, names []string) error {
	if s.interactive || s.yes {
		return nil
	}
	return fmt.Errorf("%s needs a terminal or --yes; no changes made. Targets: %s", command, strings.Join(names, ", "))
}

func (s *confirmationSession) confirm(prompt string) bool {
	if s.yes {
		return true
	}
	return confirmFrom(s.reader, os.Stdout, prompt)
}

func confirmFrom(reader *bufio.Reader, out io.Writer, prompt string) bool {
	fmt.Fprintf(out, "%s (y/N) ", prompt)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}

func printUnsavedWork(deps *cli.Deps, worktrees []cli.WorktreeInfo, resetTarget string) error {
	for _, wt := range worktrees {
		fmt.Printf("  %s (%s):\n", wt.Name, wt.Path)
		status, err := runGit(deps, wt.Path, "status", "--short", "--untracked-files=all")
		if err != nil {
			return fmt.Errorf("reading status for %s: %w", wt.Name, err)
		}
		if strings.TrimSpace(status) == "" {
			fmt.Println("    working tree clean")
		} else {
			for _, line := range strings.Split(strings.TrimRight(status, "\n"), "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
		commits, err := runGit(deps, wt.Path, "log", "--oneline", "@{upstream}..HEAD")
		commitLabel := "Unpushed commits"
		baseBranch := resetTarget
		if baseBranch == "" && wt.Repo != nil {
			baseBranch = wt.Repo.DefaultBranch
		}
		if err != nil && baseBranch != "" {
			base := "origin/" + baseBranch
			commits, err = runGit(deps, wt.Path, "log", "--oneline", base+"..HEAD")
			commitLabel = "Local commits ahead of " + base
		}
		if err == nil && strings.TrimSpace(commits) != "" {
			fmt.Printf("    %s:\n", commitLabel)
			for _, line := range strings.Split(strings.TrimRight(commits, "\n"), "\n") {
				fmt.Printf("      %s\n", line)
			}
		} else if err != nil {
			fmt.Println("    Unpushed commits: upstream not configured")
		}
	}
	return nil
}
