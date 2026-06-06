package fleet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func sampleCreateParams() backend.CreateParams {
	est := 45
	return backend.CreateParams{
		Title:       "dup me",
		Description: "desc",
		IssueType:   "bug",
		Priority:    2,
		Labels:      []string{"a", "b"},
		// Fields fleet-db drops from the create body:
		ExternalRef:      "gh-42",
		EstimatedMinutes: &est,
		CreatedBy:        "agent-1",
		Dependencies:     []string{"dep-1"},
	}
}

// TestCreate_IdempotencyHeadersAndWireBody locks the two invariants the CLI
// key derivation depends on:
//  1. the idempotency values travel as headers, never body fields (fleet-db
//     strict decode would 400);
//  2. the wire body bytes equal json.Marshal(params.FleetCreateBody()) —
//     the exact byte sequence the CLI hashes into the default key, so a
//     default key can never trip fleet-db's body-fingerprint 409.
func TestCreate_IdempotencyHeadersAndWireBody(t *testing.T) {
	params := sampleCreateParams()
	params.IdempotencyKey = "key-123"
	params.Force = true

	var gotBody []byte
	var gotKey, gotForce string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotKey = r.Header.Get("X-Idempotency-Key")
		gotForce = r.Header.Get("X-Idempotency-Force")
		respondOK(w, types.Issue{ID: "TEST-1", Title: params.Title})
	})
	defer ts.Close()

	if _, err := fb.Create(context.Background(), params); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if gotKey != "key-123" || gotForce != "true" {
		t.Errorf("headers = key %q force %q, want key-123/true", gotKey, gotForce)
	}

	// Header-only: the body must not leak the idempotency fields.
	var asMap map[string]any
	if err := json.Unmarshal(gotBody, &asMap); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	for _, k := range []string{"idempotency_key", "IdempotencyKey", "force", "Force"} {
		if _, ok := asMap[k]; ok {
			t.Errorf("body leaked field %q", k)
		}
	}

	// Wire bytes == the projection the CLI hashes. Compare via re-marshal of
	// the request's JSON (BuildJSONRequest may append a newline).
	wantBytes, err := json.Marshal(params.FleetCreateBody())
	if err != nil {
		t.Fatalf("marshal projection: %v", err)
	}
	var want, got any
	if err := json.Unmarshal(wantBytes, &want); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if err := json.Unmarshal(gotBody, &got); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	wantCanon, _ := json.Marshal(want)
	gotCanon, _ := json.Marshal(got)
	if string(wantCanon) != string(gotCanon) {
		t.Errorf("wire body diverges from CreateBodyForParams:\n got: %s\nwant: %s", gotCanon, wantCanon)
	}
}

func TestCreate_NoIdempotencyHeadersWhenUnset(t *testing.T) {
	params := sampleCreateParams() // no key, no force

	var sawKey, sawForce bool
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, sawKey = r.Header["X-Idempotency-Key"]
		_, sawForce = r.Header["X-Idempotency-Force"]
		respondOK(w, types.Issue{ID: "TEST-1"})
	})
	defer ts.Close()

	if _, err := fb.Create(context.Background(), params); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sawKey || sawForce {
		t.Errorf("unset idempotency params must not send headers (key=%v force=%v)", sawKey, sawForce)
	}
}

func TestCreate_ReplayedResponseStillReturnsIssue(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Idempotency-Replayed", "true")
		respondOK(w, types.Issue{ID: "TEST-1", Title: "dup me"})
	})
	defer ts.Close()

	params := sampleCreateParams()
	params.IdempotencyKey = "key-123"
	issue, err := fb.Create(context.Background(), params)
	if err != nil {
		t.Fatalf("Create on replay: %v", err)
	}
	if issue.ID != "TEST-1" {
		t.Errorf("replayed issue ID = %q, want TEST-1", issue.ID)
	}
}
