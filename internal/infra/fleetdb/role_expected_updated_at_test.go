package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRoleStoreUpdateExpectedUpdatedAtWireAndConflict(t *testing.T) {
	revision := time.Date(2026, time.August, 14, 12, 34, 56, 789, time.UTC)
	var sent map[string]any
	client, closeFn := newRoleTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"stale_revision","message":"role changed"}}`))
	})
	defer closeFn()

	_, err := client.Roles().Update(t.Context(), "WS", "worker", store.RoleUpdate{ExpectedUpdatedAt: &revision})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Update error = %v, want ErrConflict", err)
	}
	if sent["expected_updated_at"] != revision.Format(time.RFC3339Nano) {
		t.Fatalf("expected_updated_at = %#v, want %q", sent["expected_updated_at"], revision.Format(time.RFC3339Nano))
	}
}

func TestRoleStoreUpdateOmitsExpectedUpdatedAtWhenAbsent(t *testing.T) {
	var sent map[string]any
	client, closeFn := newRoleTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_ = json.NewEncoder(w).Encode(roleWire{WorkspaceKey: "WS", Name: "worker"})
	})
	defer closeFn()
	if _, err := client.Roles().Update(t.Context(), "WS", "worker", store.RoleUpdate{}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, ok := sent["expected_updated_at"]; ok {
		t.Fatalf("unexpected expected_updated_at in %#v", sent)
	}
}
