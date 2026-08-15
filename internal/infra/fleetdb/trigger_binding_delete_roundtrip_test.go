package fleetdb_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/store/storetest"
)

func TestFleetDBTriggerBindingDeleteConformanceRoundTrip(t *testing.T) {
	if os.Getenv("LOOM_RUN_EMBEDDED_SMOKE") != "1" {
		t.Skip("set LOOM_RUN_EMBEDDED_SMOKE=1 (with a freshly built fleet-db binary) to run the trigger-binding delete round-trip")
	}
	if diag := bootstrap.DiagnoseFleetDBBinary(); diag.Err != nil {
		t.Skipf("fleet-db binary unavailable: %v", diag.Err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	emb, err := bootstrap.StartEmbedded(ctx, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("StartEmbedded: %v", err)
	}
	t.Cleanup(func() { _ = emb.Stop() })
	client, err := fleetdb.New(fleetdb.Config{BaseURL: emb.URL(), Actor: "binding-delete-roundtrip"})
	if err != nil {
		t.Fatalf("fleetdb client: %v", err)
	}
	var seq atomic.Int64
	storetest.RunTriggerBindingDeleteConformance(t, func(t testing.TB) *storetest.TriggerBindingDeleteHarness {
		ws := fmt.Sprintf("TBDRT%d", seq.Add(1))
		if _, err := client.Workspaces().Create(ctx, store.WorkspaceCreate{Key: ws, Name: ws}); err != nil {
			t.Fatalf("create workspace %s: %v", ws, err)
		}
		return &storetest.TriggerBindingDeleteHarness{Workspace: ws, Store: client}
	})
}
