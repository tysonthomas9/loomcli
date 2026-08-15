package memstore

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRoleStoreUpdateExpectedUpdatedAt(t *testing.T) {
	st := New()
	role, err := st.Roles().Create(t.Context(), store.RoleCreate{WorkspaceKey: "WS", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	prompt := "new prompt"
	stale := role.UpdatedAt.Add(-time.Second)
	if _, err := st.Roles().Update(t.Context(), "WS", "worker", store.RoleUpdate{
		ExpectedUpdatedAt: &stale,
		Prompt:            &prompt,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale Update error = %v, want ErrConflict", err)
	}
	unchanged, err := st.Roles().Get(t.Context(), "WS", "worker")
	if err != nil || unchanged.Prompt != "" {
		t.Fatalf("stale update mutated role: %+v, %v", unchanged, err)
	}
	updated, err := st.Roles().Update(t.Context(), "WS", "worker", store.RoleUpdate{
		ExpectedUpdatedAt: &role.UpdatedAt,
		Prompt:            &prompt,
	})
	if err != nil || updated.Prompt != prompt {
		t.Fatalf("matching Update = %+v, %v", updated, err)
	}
}
