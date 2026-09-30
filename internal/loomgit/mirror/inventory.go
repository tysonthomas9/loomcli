package mirror

import (
	"context"

	loomstatus "github.com/tysonthomas9/loomcli/internal/loomgit/status"
)

type InventorySnapshot = loomstatus.Snapshot

func InventoryStatus(ctx context.Context) (InventorySnapshot, error) {
	return loomstatus.Read(ctx)
}
