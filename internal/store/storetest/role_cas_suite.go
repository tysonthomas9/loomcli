package storetest

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// RoleCASHarness supplies an isolated workspace and store for one role CAS
// conformance case.
type RoleCASHarness struct {
	Workspace string
	Store     store.Store
}

// RunRoleCASConformance pins optimistic role-update concurrency at the Store
// seam. HTTP status mapping is a separate client-wire concern.
func RunRoleCASConformance(t *testing.T, newHarness func(testing.TB) *RoleCASHarness) {
	t.Helper()
	t.Run("StaleExpectedUpdatedAtConflicts", func(t *testing.T) {
		h, role := seedRoleCASFixture(t, newHarness)
		stale := role.UpdatedAt.Add(-time.Second)
		prompt := "stale prompt"
		if _, err := h.Store.Roles().Update(t.Context(), h.Workspace, role.Name, store.RoleUpdate{
			ExpectedUpdatedAt: &stale,
			Prompt:            &prompt,
		}); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("stale Update err = %v, want ErrConflict", err)
		}
		unchanged, err := h.Store.Roles().Get(t.Context(), h.Workspace, role.Name)
		if err != nil {
			t.Fatalf("Get after stale Update: %v", err)
		}
		if unchanged.Prompt != "" {
			t.Fatalf("stale Update changed prompt to %q", unchanged.Prompt)
		}
	})

	t.Run("MatchingExpectedUpdatedAtSucceeds", func(t *testing.T) {
		h, role := seedRoleCASFixture(t, newHarness)
		prompt := "matching prompt"
		updated, err := h.Store.Roles().Update(t.Context(), h.Workspace, role.Name, store.RoleUpdate{
			ExpectedUpdatedAt: &role.UpdatedAt,
			Prompt:            &prompt,
		})
		if err != nil {
			t.Fatalf("matching Update: %v", err)
		}
		if updated.Prompt != prompt {
			t.Fatalf("matching Update prompt = %q, want %q", updated.Prompt, prompt)
		}
	})

	t.Run("AbsentExpectedUpdatedAtBypassesCAS", func(t *testing.T) {
		h, role := seedRoleCASFixture(t, newHarness)
		prompt := "unconditional prompt"
		updated, err := h.Store.Roles().Update(t.Context(), h.Workspace, role.Name, store.RoleUpdate{Prompt: &prompt})
		if err != nil {
			t.Fatalf("unconditional Update: %v", err)
		}
		if updated.Prompt != prompt {
			t.Fatalf("unconditional Update prompt = %q, want %q", updated.Prompt, prompt)
		}
	})
}

func seedRoleCASFixture(t testing.TB, newHarness func(testing.TB) *RoleCASHarness) (*RoleCASHarness, *domain.Role) {
	t.Helper()
	h := newHarness(t)
	if h == nil || h.Store == nil || h.Workspace == "" {
		t.Fatal("role CAS harness requires store and workspace")
	}
	role, err := h.Store.Roles().Create(t.Context(), store.RoleCreate{WorkspaceKey: h.Workspace, Name: "worker"})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	return h, role
}
