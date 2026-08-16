package readprojection

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

// BindingRunReader is the read-model port for one trigger binding's run
// history. Results are bounded and use the UI's canonical newest-first order.
type BindingRunReader interface {
	ListByBinding(context.Context, string, string, int) ([]*execution.DriverRunRecord, error)
}

type bindingRunReader struct {
	runs execution.DriverRunStore
}

// NewBindingRunReader closes the persistence-filter seam at composition so
// HTTP adapters receive neither DriverRunStore nor DriverRunFilter.
func NewBindingRunReader(runs execution.DriverRunStore) BindingRunReader {
	if runs == nil {
		return nil
	}
	return bindingRunReader{runs: runs}
}

func (reader bindingRunReader) ListByBinding(
	ctx context.Context,
	workspace, bindingID string,
	limit int,
) ([]*execution.DriverRunRecord, error) {
	runs, err := reader.runs.List(ctx, workspace, execution.DriverRunFilter{BindingID: bindingID, Limit: limit})
	if err != nil {
		return nil, err
	}
	execution.SortDriverRunsNewestFirst(runs)
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}
