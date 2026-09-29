package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

type Local struct{ store *journal.SQLite }

func OpenLocal() (*Local, error) {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	return &Local{store: store}, nil
}

func (l *Local) Close() error { return l.store.Close() }

func (l *Local) Submit(ctx context.Context, workspace, change string, number int, headSHA, kind, reason string, actor Actor) (loomgit.Verdict, error) {
	return Submit(ctx, l.store, workspace, change, number, headSHA, kind, reason, actor)
}

func (l *Local) RequireVerdict(ctx context.Context, workspace, change string, number int, headSHA, operation, targetLead string) error {
	return RequireVerdict(ctx, l.store, workspace, change, number, headSHA, operation, targetLead)
}

type TaskRevision struct {
	ChangeID   string `json:"change_id"`
	Number     int    `json:"number"`
	HeadSHA    string `json:"head_sha"`
	Outcome    string `json:"outcome"`
	Incomplete bool   `json:"incomplete"`
	Verdict    string `json:"verdict,omitempty"`
}

func (l *Local) TaskRevisions(ctx context.Context, workspace, task string) ([]TaskRevision, error) {
	revisions, err := l.store.ListTaskRevisions(ctx, workspace, task)
	if err != nil {
		return nil, err
	}
	out := make([]TaskRevision, 0, len(revisions))
	for _, r := range revisions {
		i := TaskRevision{ChangeID: r.Change, Number: r.Number, HeadSHA: r.HeadSHA, Outcome: r.Outcome, Incomplete: r.Incomplete}
		if v, err := l.store.LatestVerdict(ctx, r); err == nil {
			i.Verdict = v.Kind
		} else if !errors.Is(err, journal.ErrNotFound) {
			return nil, err
		}
		out = append(out, i)
	}
	return out, nil
}

func IsNotFound(err error) bool {
	return errors.Is(err, journal.ErrNotFound) || errors.Is(err, os.ErrNotExist)
}
