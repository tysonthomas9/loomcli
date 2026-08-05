package bootstrap_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestEmbeddedFleetDBPersistenceSmoke(t *testing.T) {
	if os.Getenv("LOOM_RUN_EMBEDDED_SMOKE") != "1" {
		t.Skip("set LOOM_RUN_EMBEDDED_SMOKE=1 to run real embedded fleet-db persistence smoke")
	}
	if diag := bootstrap.DiagnoseFleetDBBinary(); diag.Err != nil {
		t.Skipf("fleet-db binary unavailable: %v", diag.Err)
	}

	dataDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	emb, err := bootstrap.StartEmbedded(ctx, dataDir, slog.Default())
	if err != nil {
		t.Fatalf("StartEmbedded first: %v", err)
	}
	client, err := emb.NewClient(fleetdb.Config{Actor: "embedded-smoke"})
	if err != nil {
		t.Fatalf("fleetdb client: %v", err)
	}
	if err := client.RequireCapabilities(ctx, []string{fleetdb.WorkflowCatalogVersionLifecycleCapability}); err != nil {
		t.Fatalf("embedded FleetDB capability readiness: %v", err)
	}
	if _, err := client.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "SMOKE", Name: "Smoke"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := client.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "SMOKE", Name: "repo"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := client.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "SMOKE", Name: "task"}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("create role: %v", err)
	}
	if _, err := client.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: "SMOKE", Name: "worker", RoleName: "task"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := emb.Stop(); err != nil {
		t.Fatalf("Stop first: %v", err)
	}

	emb2, err := bootstrap.StartEmbedded(ctx, dataDir, slog.Default())
	if err != nil {
		t.Fatalf("StartEmbedded second: %v", err)
	}
	defer emb2.Stop()
	client2, err := emb2.NewClient(fleetdb.Config{Actor: "embedded-smoke"})
	if err != nil {
		t.Fatalf("fleetdb client 2: %v", err)
	}
	if _, err := client2.Workspaces().Get(ctx, "SMOKE"); err != nil {
		t.Fatalf("workspace did not survive restart: %v", err)
	}
	if _, err := client2.Repos().Get(ctx, "SMOKE", "repo"); err != nil {
		t.Fatalf("repo did not survive restart: %v", err)
	}
	if _, err := client2.Agents().Get(ctx, "SMOKE", "worker"); err != nil {
		t.Fatalf("agent did not survive restart: %v", err)
	}
}
