package cmdstore

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestOpenStoreWithStoreAndActiveWorkspace(t *testing.T) {
	requireCmdstoreFleetDB(t)
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv(bootstrap.EnvFleetDBURL, "")
	t.Setenv(bootstrap.EnvFleetDBActor, "cmdstore-open-test")
	t.Setenv(bootstrap.EnvWorkspace, "WS")

	handle, err := OpenStore(context.Background())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if handle.Store == nil {
		t.Fatal("OpenStore returned nil Store")
	}
	if _, err := handle.Store.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close handle: %v", err)
	}

	var sawStore bool
	if err := WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		sawStore = h.Store != nil && ctx != nil
		return nil
	}); err != nil {
		t.Fatalf("WithStore: %v", err)
	}
	if !sawStore {
		t.Fatal("WithStore did not pass a usable store")
	}

	var gotWorkspace string
	if err := WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		gotWorkspace = ws
		if _, err := h.Store.Workspaces().Get(ctx, ws); err != nil {
			t.Fatalf("get active workspace: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithActiveWorkspace: %v", err)
	}
	if gotWorkspace != "WS" {
		t.Fatalf("active workspace = %q, want WS", gotWorkspace)
	}
}

func TestOpenStoreMissingConfigDir(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	_, err := OpenStore(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot resolve loom directory") {
		t.Fatalf("OpenStore missing config dir err = %v", err)
	}
}

func requireCmdstoreFleetDB(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("fleet-db"); err == nil {
		return
	}
	if _, err := exec.LookPath("fleet-db.exe"); err == nil {
		return
	}
	if _, err := exec.LookPath("fleet-db"); err != nil && testing.Short() {
		t.Skip("fleet-db binary not available")
	}
}
