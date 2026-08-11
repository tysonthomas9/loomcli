package readprojection

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// BindingRunReader is the read-model port for one trigger binding's run
// history. Results are bounded and use the UI's canonical newest-first order.
type BindingRunReader interface {
	ListByBinding(context.Context, string, string, int) ([]*domain.DriverRun, error)
}

type bindingRunReader struct {
	runs store.DriverRunStore
}

// NewBindingRunReader closes the persistence-filter seam at composition so
// HTTP adapters receive neither DriverRunStore nor DriverRunFilter.
func NewBindingRunReader(runs store.DriverRunStore) BindingRunReader {
	if runs == nil {
		return nil
	}
	return bindingRunReader{runs: runs}
}

func (reader bindingRunReader) ListByBinding(
	ctx context.Context,
	workspace, bindingID string,
	limit int,
) ([]*domain.DriverRun, error) {
	runs, err := reader.runs.List(ctx, workspace, store.DriverRunFilter{BindingID: bindingID, Limit: limit})
	if err != nil {
		return nil, err
	}
	store.SortDriverRunsNewestFirst(runs)
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}
