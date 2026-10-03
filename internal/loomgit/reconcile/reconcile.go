package reconcile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

// Recoverer is the narrow handoff to an operation's owner. Reconcile never
// reaches into apply or workspace recovery internals.
type Recoverer interface {
	Recover(context.Context) error
}

type RecoverFunc func(context.Context) error

func (recover RecoverFunc) Recover(ctx context.Context) error { return recover(ctx) }

type Handlers struct {
	Workspace Recoverer
	Apply     Recoverer
	Landing   Recoverer
}

// Handled reports whether reconcile has a recovery owner for a journal
// operation. Status shows every other open request as needing attention.
func Handled(operation string) bool {
	switch operation {
	case "ensure_workspace", "attach_workspace_repos", "apply":
		return true
	}
	return false
}

// RunOnce classifies open journal entries before invoking any recovery owner.
// Unhandled entries retain their fences for explicit repair. They name no
// workspace, so they hold back workspace and apply recovery everywhere; landing
// still runs, and one owner's failure never skips the next owner.
func RunOnce(ctx context.Context, handlers Handlers) error {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	st, err := journal.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	entries, err := st.OpenEntries(ctx)
	if err != nil {
		return err
	}
	var workspaceOpen bool
	var failures []error
	for _, entry := range entries {
		switch entry.Operation {
		case "ensure_workspace", "attach_workspace_repos":
			workspaceOpen = true
			if handlers.Workspace != nil {
				continue
			}
		case "apply":
			if handlers.Apply != nil {
				continue
			}
		}
		failures = append(failures, loomgit.NewError(loomgit.AttentionRequired,
			fmt.Sprintf("journal request %q has no recovery handler for %q", entry.RequestID, entry.Operation), nil))
	}
	if len(failures) == 0 {
		if workspaceOpen {
			failures = append(failures, handlers.Workspace.Recover(ctx))
		}
		if handlers.Apply != nil {
			failures = append(failures, handlers.Apply.Recover(ctx))
		}
	}
	if handlers.Landing != nil {
		failures = append(failures, handlers.Landing.Recover(ctx))
	}
	return errors.Join(failures...)
}
