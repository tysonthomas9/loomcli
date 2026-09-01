package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestSkillMaterializationLeaseStoreAcquire(t *testing.T) {
	expiresAt := time.Now().UTC().Add(15 * time.Second).Truncate(time.Nanosecond)
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/skill-materialization-leases" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body acquireSkillMaterializationLeaseBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Holder != "lead@host#42" || body.TargetKey != "target-sha256" || body.TTLSeconds != 15 ||
			!reflect.DeepEqual(body.TreeRevisions, []string{"wft1_a", "wft1_b"}) {
			t.Fatalf("body = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(domain.SkillMaterializationLease{
			Token: "token-1", TargetKey: body.TargetKey, Holder: body.Holder, ExpiresAt: expiresAt,
		})
	})
	defer closeFn()

	got, err := client.SkillMaterializationLeases().Acquire(t.Context(), store.SkillMaterializationLeaseAcquire{
		WorkspaceKey: "WS", Holder: "lead@host#42", TargetKey: "target-sha256",
		TreeRevisions: []string{"wft1_a", "wft1_b"}, TTL: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got.Token != "token-1" || got.TargetKey != "target-sha256" || got.Holder != "lead@host#42" || !got.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("lease = %+v", got)
	}
}

func TestSkillMaterializationLeaseStoreAcquireEncodesExplicitEmptyRevisionSet(t *testing.T) {
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body acquireSkillMaterializationLeaseBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.TreeRevisions == nil || len(body.TreeRevisions) != 0 {
			t.Fatalf("tree_revisions = %#v, want explicit []", body.TreeRevisions)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(domain.SkillMaterializationLease{Token: "empty-token", TreeRevisions: []string{}})
	})
	defer closeFn()

	if _, err := client.SkillMaterializationLeases().Acquire(t.Context(), store.SkillMaterializationLeaseAcquire{
		WorkspaceKey: "WS", Holder: "lead@host#42", TargetKey: "target-sha256", TTL: time.Second,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSkillMaterializationLeaseStoreConflictSurfacesMetadata(t *testing.T) {
	expiresAt := time.Now().UTC().Add(10 * time.Second).Truncate(time.Nanosecond)
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSkillError(w, http.StatusConflict, skillMaterializationLeaseConflictCode,
			"skill materialization target is leased", map[string]string{
				"holder": "worker@host#7", "expires_at": expiresAt.Format(time.RFC3339Nano),
			})
	})
	defer closeFn()

	_, err := client.SkillMaterializationLeases().Acquire(t.Context(), store.SkillMaterializationLeaseAcquire{
		WorkspaceKey: "WS", Holder: "lead@host#42", TargetKey: "target-sha256",
	})
	var conflict *domain.SkillMaterializationLeaseConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, domain.ErrSkillMaterializationLeaseConflict) {
		t.Fatalf("Acquire error = %v, want typed lease conflict", err)
	}
	if conflict.Holder != "worker@host#7" || !conflict.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("conflict = %+v", conflict)
	}
}

func TestSkillMaterializationLeaseStoreUnavailableIsRecognized(t *testing.T) {
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSkillError(w, http.StatusServiceUnavailable, skillMaterializationLeaseStoreUnavailableCode,
			"skill materialization lease store unavailable", nil)
	})
	defer closeFn()

	_, err := client.SkillMaterializationLeases().Acquire(t.Context(), store.SkillMaterializationLeaseAcquire{
		WorkspaceKey: "WS", Holder: "lead@host#42", TargetKey: "target-sha256",
	})
	if !errors.Is(err, domain.ErrSkillMaterializationLeaseStoreUnavailable) {
		t.Fatalf("Acquire error = %v, want lease store unavailable", err)
	}
}

func TestSkillMaterializationLeaseStoreRenew(t *testing.T) {
	expiresAt := time.Now().UTC().Add(30 * time.Second).Truncate(time.Nanosecond)
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/WS/skill-materialization-leases/target-sha256" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body renewSkillMaterializationLeaseBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Token != "token-1" || body.TTLSeconds != 30 {
			t.Fatalf("body = %+v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"expires_at": expiresAt})
	})
	defer closeFn()

	got, err := client.SkillMaterializationLeases().Renew(t.Context(), "WS", "target-sha256", "token-1", 30*time.Second)
	if err != nil || !got.Equal(expiresAt) {
		t.Fatalf("Renew = %v, %v", got, err)
	}
}

func TestSkillMaterializationLeaseStoreReleaseIsIdempotent(t *testing.T) {
	requests := 0
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/WS/skill-materialization-leases/target-sha256" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body releaseSkillMaterializationLeaseBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Token != "token-1" {
			t.Fatalf("token = %q", body.Token)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	defer closeFn()

	for range 2 {
		if err := client.SkillMaterializationLeases().Release(t.Context(), "WS", "target-sha256", "token-1"); err != nil {
			t.Fatalf("Release: %v", err)
		}
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}
