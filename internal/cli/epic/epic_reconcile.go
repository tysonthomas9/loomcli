package epic

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/stacklock"
)

type StackPublisher func(context.Context, string, string, string, []string) ([]publish.Result, error)

func ReconcileEpicStack(ctx context.Context, workspace, lead string, publisher StackPublisher) error {
	results, err := publisher(stacklock.ForEpicReconcile(ctx), workspace, publish.LeadStackID(lead), lead, nil)
	if err != nil {
		return err
	}
	for _, result := range results {
		fmt.Printf("[epic-run] published %s  %s\n", result.Revision.Change, result.PRURL)
	}
	return nil
}
