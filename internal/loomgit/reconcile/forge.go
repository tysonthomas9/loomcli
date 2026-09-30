package reconcile

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/loomgit/feedback"
)

type ForgeEvent = feedback.ForgeEvent

// IngestForgeEvent reconciles a verified forge delivery with published changes.
func IngestForgeEvent(ctx context.Context, workspace string, event ForgeEvent) error {
	return feedback.Ingest(ctx, workspace, event)
}
