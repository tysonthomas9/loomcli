package memstore

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestSkillMaterializationLeaseStoreSerializesEmptyProjection(t *testing.T) {
	st := New()
	leasing := st.SkillMaterializationLeases()
	request := store.SkillMaterializationLeaseAcquire{
		WorkspaceKey: "WS", Holder: "lead@test#1", TargetKey: "target",
		TreeRevisions: []string{}, TTL: time.Minute,
	}
	lease, err := leasing.Acquire(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if lease.TreeRevisions == nil || len(lease.TreeRevisions) != 0 {
		t.Fatalf("tree revisions = %#v, want explicit empty set", lease.TreeRevisions)
	}
	if _, err := leasing.Acquire(t.Context(), request); !errors.Is(err, domain.ErrSkillMaterializationLeaseConflict) {
		t.Fatalf("second acquire error = %v, want conflict", err)
	}
	if _, err := leasing.Renew(t.Context(), "WS", "target", lease.Token, time.Minute); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if err := leasing.Release(t.Context(), "WS", "target", lease.Token); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := leasing.Acquire(t.Context(), request); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}
