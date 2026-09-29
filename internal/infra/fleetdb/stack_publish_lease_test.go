package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/stackstore/stackwire"
)

func newLeaseTestClient(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	c, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		srv.Close()
		t.Fatalf("New: %v", err)
	}
	return c, srv.Close
}

func TestStackPublishLeaseAcquireRenewRelease(t *testing.T) {
	var gotHolder string
	var renewToken string
	var releaseHeader string
	client, closeFn := newLeaseTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		want := "/api/v1/WS/stacks/" + pathEscape("epic:E") + "/publish-lease"
		if r.URL.Path != want {
			t.Fatalf("path = %s, want %s", r.URL.Path, want)
		}
		switch r.Method {
		case http.MethodPost:
			var body struct {
				Holder string `json:"holder"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotHolder = body.Holder
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(stackwire.PublishLease{
				Token: "t1", StackID: "epic:E", Holder: body.Holder, Generation: 1,
				ExpiresAt: time.Now().Add(time.Minute), ReuseAfter: time.Now().Add(2 * time.Minute),
			})
		case http.MethodPut:
			var body struct {
				Token string `json:"token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			renewToken = body.Token
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(stackwire.PublishLease{
				Token: body.Token, StackID: "epic:E", Holder: "h", Generation: 1,
				ExpiresAt: time.Now().Add(time.Minute), ReuseAfter: time.Now().Add(2 * time.Minute),
			})
		case http.MethodDelete:
			releaseHeader = r.Header.Get("X-Lease-Token")
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("method %s", r.Method)
		}
	})
	defer closeFn()

	stacks := client.Stacks().(*StackClient)
	lease, err := stacks.AcquirePublishLease(t.Context(), "WS", "epic:E", stackwire.AcquirePublishLease{Holder: "a@host#1"})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if gotHolder != "a@host#1" || lease.Token != "t1" {
		t.Fatalf("holder=%q lease=%+v", gotHolder, lease)
	}
	if _, err := stacks.RenewPublishLease(t.Context(), "WS", "epic:E", stackwire.RenewPublishLease{Token: "t1"}); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if renewToken != "t1" {
		t.Fatalf("renew token = %q", renewToken)
	}
	if err := stacks.ReleasePublishLease(t.Context(), "WS", "epic:E", "t1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if releaseHeader != "t1" {
		t.Fatalf("release header = %q", releaseHeader)
	}
}

func TestStackPublishLeaseBusySurfacesHolderAndReuseAfter(t *testing.T) {
	reuse := time.Now().UTC().Add(90 * time.Second)
	expires := time.Now().UTC().Add(30 * time.Second)
	client, closeFn := newLeaseTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "stack_publish_lease_busy",
				"message": "stack publish lease is busy",
				"meta": map[string]string{
					"holder":      "other@host#9",
					"generation":  "3",
					"expires_at":  expires.Format(time.RFC3339Nano),
					"reuse_after": reuse.Format(time.RFC3339Nano),
				},
			},
		})
	})
	defer closeFn()

	_, err := client.Stacks().(*StackClient).AcquirePublishLease(t.Context(), "WS", "S", stackwire.AcquirePublishLease{Holder: "me"})
	var busy *domain.StackPublishLeaseBusyError
	if !errors.As(err, &busy) || !errors.Is(err, domain.ErrStackPublishLeaseBusy) {
		t.Fatalf("err = %v", err)
	}
	if busy.Holder != "other@host#9" || busy.Generation != 3 || busy.RetryAt().IsZero() {
		t.Fatalf("busy = %+v", busy)
	}
}

func TestStackPublishLeaseStoreUnavailableFailsClosed(t *testing.T) {
	client, closeFn := newLeaseTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "stack_publish_lease_store_unavailable",
				"message": "stack publish lease store unavailable",
			},
		})
	})
	defer closeFn()

	_, err := client.Stacks().(*StackClient).AcquirePublishLease(t.Context(), "WS", "S", stackwire.AcquirePublishLease{Holder: "me"})
	if !errors.Is(err, domain.ErrStackPublishLeaseStoreUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestStackPublishLeaseMissingRouteFailsClosed(t *testing.T) {
	client, closeFn := newLeaseTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "not_found", "message": "not found"},
		})
	})
	defer closeFn()

	_, err := client.Stacks().(*StackClient).AcquirePublishLease(t.Context(), "WS", "S", stackwire.AcquirePublishLease{Holder: "me"})
	if !errors.Is(err, domain.ErrStackPublishLeaseStoreUnavailable) {
		t.Fatalf("err = %v, want unavailable (fail closed)", err)
	}
}

func TestStackPublishLeaseRenewExpiredIsLeaseLost(t *testing.T) {
	client, closeFn := newLeaseTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "lease_expired",
				"message": "stack publish lease expired",
			},
		})
	})
	defer closeFn()

	_, err := client.Stacks().(*StackClient).RenewPublishLease(t.Context(), "WS", "S", stackwire.RenewPublishLease{Token: "t1"})
	// Transport maps 410 → ErrGone; store admission maps Gone → lease lost.
	if !errors.Is(err, domain.ErrGone) {
		t.Fatalf("err = %v, want ErrGone (410 lease_expired)", err)
	}
}

func TestStackPublishLeaseRenewNotFoundIsLeaseLost(t *testing.T) {
	client, closeFn := newLeaseTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "not_found", "message": "lease not found"},
		})
	})
	defer closeFn()

	_, err := client.Stacks().(*StackClient).RenewPublishLease(t.Context(), "WS", "S", stackwire.RenewPublishLease{Token: "t1"})
	if !errors.Is(err, domain.ErrStackPublishLeaseLost) {
		t.Fatalf("err = %v, want lease lost (renew 404)", err)
	}
}

func TestStackPublishLeaseReleaseNotFoundIdempotent(t *testing.T) {
	client, closeFn := newLeaseTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "not_found", "message": "not found"},
		})
	})
	defer closeFn()

	if err := client.Stacks().(*StackClient).ReleasePublishLease(t.Context(), "WS", "S", "t1"); err != nil {
		t.Fatalf("Release: %v (want idempotent success)", err)
	}
}

func TestStackPublishLeaseStaleTokenMismatch(t *testing.T) {
	client, closeFn := newLeaseTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "stack_publish_lease_token_mismatch",
				"message": "stack publish lease token mismatch",
			},
		})
	})
	defer closeFn()

	err := client.Stacks().(*StackClient).ReleasePublishLease(t.Context(), "WS", "S", "stale")
	if !errors.Is(err, domain.ErrStackPublishLeaseTokenMismatch) {
		t.Fatalf("err = %v, want token mismatch", err)
	}
}
