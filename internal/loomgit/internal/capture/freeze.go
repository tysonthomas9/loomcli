package capture

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

type FreezeParams struct {
	Workspace string
	Attempt   string
	TaskID    string
	ChangeID  string
	Revision  string
	BaseSHA   string
	HeadSHA   string // capture SHA when Capture made one, otherwise the latest agent commit
}

// RewriteSource creates the source chain without publishing a revision ref.
// The caller can verify and record it before installing refs.
func RewriteSource(ctx context.Context, runner loomgit.RepoStore, p FreezeParams) (string, error) {
	if _, err := refname.RevisionHead(p.Workspace, p.ChangeID, p.Revision); err != nil {
		return "", err
	}
	commits, err := runner.Run(ctx, "rev-list", "--reverse", p.BaseSHA+".."+p.HeadSHA)
	if err != nil {
		return "", err
	}
	parent := p.BaseSHA
	for _, commit := range strings.Fields(string(commits)) {
		meta, err := runner.Run(ctx, "show", "-s", "--format=%an%x00%ae%x00%aI%x00%B", commit)
		if err != nil {
			return "", err
		}
		parts := strings.SplitN(string(meta), "\x00", 4)
		if len(parts) != 4 {
			return "", fmt.Errorf("commit %s has invalid author metadata", commit)
		}
		message := strings.TrimRight(parts[3], "\n")
		for _, trailer := range [][2]string{
			{"Loom-Task", p.TaskID}, {"Loom-Attempt", p.Attempt},
			{"Loom-Change-Id", p.ChangeID}, {"Loom-Revision", p.Revision},
		} {
			key, value := trailer[0], trailer[1]
			if !strings.Contains(message, "\n"+key+": ") {
				if !strings.Contains(message, "\n\n") {
					message += "\n"
				}
				message += "\n" + key + ": " + value
			}
		}
		tree, err := git(ctx, runner, "rev-parse", commit+"^{tree}")
		if err != nil {
			return "", err
		}
		env := map[string]string{"GIT_AUTHOR_NAME": parts[0], "GIT_AUTHOR_EMAIL": parts[1], "GIT_AUTHOR_DATE": parts[2]}
		out, err := runner.RunWithEnv(ctx, env, "commit-tree", tree, "-p", parent, "-m", message)
		if err != nil {
			return "", err
		}
		parent = strings.TrimSpace(string(out))
	}
	return parent, nil
}
