package taskcopy

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
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
			tip, err := agentcapture.WorkingAreaTip(ctx, a)
			return a.Path, tip, err
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
