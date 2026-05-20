package memstore

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestStoreCloseAndWorkspaceGetByName(t *testing.T) {
	st := New()
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ctx := t.Context()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	ws, err := st.Workspaces().GetByName(ctx, "Workspace")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if ws.Key != "WS" {
		t.Fatalf("workspace key = %q, want WS", ws.Key)
	}
	if _, err := st.Workspaces().GetByName(ctx, "Missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByName missing err = %v", err)
	}
}
