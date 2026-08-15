package fleetdb_test

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/store/storetest"
)

func TestFleetDBRoleCASConformanceRoundTrip(t *testing.T) {
	requireEmbeddedFleetDBConformance(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	emb, err := bootstrap.StartEmbedded(ctx, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("StartEmbedded: %v", err)
	}
	t.Cleanup(func() { _ = emb.Stop() })
	client, err := fleetdb.New(fleetdb.Config{BaseURL: emb.URL(), Actor: "role-cas-roundtrip"})
	if err != nil {
		t.Fatalf("fleetdb client: %v", err)
	}
	var seq atomic.Int64
	storetest.RunRoleCASConformance(t, func(t testing.TB) *storetest.RoleCASHarness {
		ws := fmt.Sprintf("RCAS%d", seq.Add(1))
		if _, err := client.Workspaces().Create(ctx, store.WorkspaceCreate{Key: ws, Name: ws}); err != nil {
			t.Fatalf("create workspace %s: %v", ws, err)
		}
		return &storetest.RoleCASHarness{Workspace: ws, Store: client}
	})
}
