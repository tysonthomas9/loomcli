package taskcopy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

// LeadSource resolves the selected repo's durable lead checkout and current tip.
func LeadSource(ctx context.Context, workspace, lead, repo string) (string, string, error) {
	st, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return "", "", err
	}
	defer func() { _ = st.Close() }()
	areas, err := st.WorkingAreas(ctx, workspace, lead)
	if err != nil {
		return "", "", err
	}
	for _, a := range areas {
		if a.Repo == repo {
			if _, err := os.Stat(filepath.Join(a.Path, ".git")); err != nil {
				return "", "", err
			}
			r, err := gitexec.New(a.Path, gitexec.Options{ReadOnly: true})
			if err != nil {
				return "", "", err
			}
			ref, err := r.Run(ctx, "rev-parse", "--symbolic-full-name", "HEAD")
			if err != nil {
				return "", "", err
			}
			if strings.TrimSpace(string(ref)) != "refs/heads/"+a.Branch {
				return "", "", errors.New("lead working area branch changed")
			}
			sha, err := r.Run(ctx, "rev-parse", "HEAD")
			return a.Path, strings.TrimSpace(string(sha)), err
		}
	}
	return "", "", errors.New("lead working area not found")
}

// RevisionBase pins a dependent copy to the exact approved revision head.
func RevisionBase(ctx context.Context, workspace, change string, number int) (string, error) {
	if change == "" || number < 1 {
		return "", errors.New("revision change and number are required")
	}
	st, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return "", err
	}
	defer func() { _ = st.Close() }()
	rev, err := st.GetRevision(ctx, workspace, change, number)
	if err != nil {
		return "", err
	}
	if !rev.Ready || rev.HeadSHA == "" {
		return "", errors.New("revision is not ready")
	}
	return rev.HeadSHA, nil
}
