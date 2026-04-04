package beads

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// Reopen transitions a closed issue back to open status.
// It sets status to "open" via Update, then adds a comment with the reason
// (if non-empty). A comment failure is non-fatal since the status transition
// already succeeded — this mirrors the bd reopen CLI behavior.
func (b *BeadsBackend) Reopen(_ context.Context, id string, params backend.ReopenParams) error {
	if id == "" {
		return backend.ErrValidation("Reopen", "id must not be empty")
	}
	openStatus := "open"
	_, err := b.execAndCheck("Reopen", func() (*rpc.Response, error) {
		return b.client.Update(&rpc.UpdateArgs{ID: id, Status: &openStatus})
	})
	if err != nil {
		return err
	}
	if params.Reason != "" {
		// Comment is best-effort; the status transition already succeeded.
		_, _ = b.execAndCheck("Reopen", func() (*rpc.Response, error) {
			return b.client.AddComment(&rpc.CommentAddArgs{ID: id, Text: params.Reason})
		})
	}
	return nil
}
