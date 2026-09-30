package mirror

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

type IntegrityIssue struct{ Ref, SHA, State, Reason string }

// RemoteIntegrity checks every mirrored ref against its recorded provider URL.
//
//nolint:funlen // One remote read is shared by all refs at that provider URL.
func RemoteIntegrity(ctx context.Context) ([]IntegrityIssue, error) {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	store, err := journal.OpenSQLiteReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	rows, err := store.ExistingMirrorRecords(ctx)
	if err != nil {
		return nil, err
	}
	byRemote := make(map[string][]journal.MirrorRecord)
	for _, row := range rows {
		if row.SHA != "" && row.Remote != "" && row.State == "mirrored" {
			byRemote[row.Repo+"\x00"+row.Remote] = append(byRemote[row.Repo+"\x00"+row.Remote], row)
		}
	}
	var issues []IntegrityIssue
	for key, records := range byRemote {
		parts := strings.SplitN(key, "\x00", 2)
		runner, err := gitexec.New(parts[0], gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
		if err != nil {
			return nil, err
		}
		remoteCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		data, err := gitPusher{runner}.run(remoteCtx, parts[1], "ls-remote", parts[1], refname.WorkspaceRefPrefix+"*")
		cancel()
		if err != nil {
			for _, row := range records {
				issues = append(issues, IntegrityIssue{Ref: row.Ref, SHA: row.SHA, State: "integrity_unverified", Reason: err.Error()})
			}
			continue
		}
		actual := make(map[string]string)
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 {
				actual[fields[1]] = fields[0]
			}
		}
		for _, row := range records {
			if actual[row.Ref] != row.SHA {
				issues = append(issues, IntegrityIssue{Ref: row.Ref, SHA: row.SHA, State: "integrity_missing", Reason: fmt.Sprintf("provider ref is %q", actual[row.Ref])})
			}
		}
	}
	return issues, nil
}
