package taskcopy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

type LineageBase struct {
	Ref      string
	SHA      string
	Change   string
	Revision int
}

type LineageStatus struct {
	State             string
	BasedOn           LineageBase
	AvailableRevision int
	AvailableRef      string
}

type DependentLineage struct {
	Task     string
	Repo     string
	Revision int
	BaseSHA  string
}

func open() (*journal.SQLite, error) {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return journal.OpenSQLite(path)
}

// ResolveLineageBase reads the newest durable predecessor revision and verifies its
// local ref against the journal. It never fetches from a provider.
func ResolveLineageBase(ctx context.Context, repoPath, workspace, task, predecessorTask, repo string) (LineageBase, error) {
	st, err := open()
	if err != nil {
		return LineageBase{}, unresolved("open local lineage", err)
	}
	defer func() { _ = st.Close() }()
	change, number, head, err := st.LatestTaskRevision(ctx, workspace, predecessorTask, repo)
	if err != nil {
		return LineageBase{}, unresolved("predecessor has no ready revision", err)
	}
	if pinned, pinErr := st.LocalLineage(ctx, workspace, task, repo); pinErr == nil {
		if pinned.PredecessorChange != change {
			return LineageBase{}, unresolved("dependent predecessor changed", nil)
		}
		number, head = pinned.PredecessorRevision, pinned.BaseSHA
	} else if !errors.Is(pinErr, journal.ErrNotFound) {
		return LineageBase{}, unresolved("read dependent lineage", pinErr)
	}
	if abandoned, err := st.ChangeAbandoned(ctx, workspace, change); err != nil {
		return LineageBase{}, unresolved("read predecessor state", err)
	} else if abandoned {
		return LineageBase{}, loomgit.NewError(loomgit.DependencyAbandoned, "predecessor was abandoned", nil)
	}
	ref, err := refname.RevisionHead(workspace, change, strconv.Itoa(number))
	if err != nil {
		return LineageBase{}, unresolved("build predecessor revision ref", err)
	}
	runner, err := gitexec.New(repoPath, gitexec.Options{ReadOnly: true})
	if err != nil {
		return LineageBase{}, unresolved("open source repository", err)
	}
	resolved, err := runner.Run(ctx, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil || strings.TrimSpace(string(resolved)) != head {
		return LineageBase{}, unresolved(fmt.Sprintf("revision ref %s is missing or differs from journal", ref), err)
	}
	return LineageBase{Ref: ref, SHA: head, Change: change, Revision: number}, nil
}

// RecordLineageBase saves the revision actually used after the dependent copy exists.
func RecordLineageBase(ctx context.Context, workspace, task, repo string, base LineageBase) error {
	st, err := open()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return st.RecordLocalLineage(ctx, journal.LocalLineage{
		Workspace: workspace, Task: task, Repo: repo, PredecessorChange: base.Change,
		PredecessorRevision: base.Revision, BaseSHA: base.SHA,
	})
}

// ReadLineageStatus computes current status without moving the dependent's base.
func ReadLineageStatus(ctx context.Context, workspace, task, repo string) (LineageStatus, error) {
	st, err := open()
	if err != nil {
		return LineageStatus{}, err
	}
	defer func() { _ = st.Close() }()
	pinned, err := st.LocalLineage(ctx, workspace, task, repo)
	if err != nil {
		return LineageStatus{}, err
	}
	ref, err := refname.RevisionHead(workspace, pinned.PredecessorChange, strconv.Itoa(pinned.PredecessorRevision))
	if err != nil {
		return LineageStatus{}, err
	}
	result := LineageStatus{State: "current", BasedOn: LineageBase{Ref: ref, SHA: pinned.BaseSHA,
		Change: pinned.PredecessorChange, Revision: pinned.PredecessorRevision}}
	abandoned, err := st.ChangeAbandoned(ctx, workspace, pinned.PredecessorChange)
	if err != nil {
		return LineageStatus{}, err
	}
	if abandoned {
		result.State = string(loomgit.DependencyAbandoned)
		return result, nil
	}
	number, _, err := st.LatestReadyRevision(ctx, workspace, pinned.PredecessorChange)
	if err != nil {
		return LineageStatus{}, err
	}
	if number > pinned.PredecessorRevision {
		result.State = string(loomgit.Stale)
		result.AvailableRevision = number
		result.AvailableRef, err = refname.RevisionHead(workspace, pinned.PredecessorChange, strconv.Itoa(number))
	}
	return result, err
}

// AbandonChange records a deliberate predecessor abandonment for read-time status.
func AbandonChange(ctx context.Context, workspace, change string) error {
	st, err := open()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return st.AbandonChange(ctx, workspace, change)
}

// DependentsOf lists local task copies pinned to a predecessor change.
func DependentsOf(ctx context.Context, workspace, change string) ([]DependentLineage, error) {
	return DependentsOfAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"), workspace, change)
}

func DependentsOfAt(ctx context.Context, journalPath, workspace, change string) ([]DependentLineage, error) {
	st, err := journal.OpenSQLite(journalPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	stored, err := st.DependentsOf(ctx, workspace, change)
	if err != nil {
		return nil, err
	}
	dependents := make([]DependentLineage, 0, len(stored))
	for _, l := range stored {
		dependents = append(dependents, DependentLineage{
			Task: l.Task, Repo: l.Repo, Revision: l.PredecessorRevision, BaseSHA: l.BaseSHA,
		})
	}
	return dependents, nil
}

func unresolved(message string, err error) error {
	if errors.Is(err, journal.ErrNotFound) {
		err = nil
	}
	return loomgit.NewError(loomgit.LineageUnresolved, message, err)
}
