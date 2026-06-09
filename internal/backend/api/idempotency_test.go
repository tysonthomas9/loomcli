package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
)

// TestCreate_SendsIdempotencyHeaders verifies the CLI→serve hop carries the
// idempotency values as headers only — never body fields.
func TestCreate_SendsIdempotencyHeaders(t *testing.T) {
	var gotKey, gotForce string
	var gotBody []byte
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Idempotency-Key")
		gotForce = r.Header.Get("X-Idempotency-Force")
		gotBody, _ = io.ReadAll(r.Body)
		respondOK(w, gen.IssueResponse{Id: "loom-1", Title: "t"})
	})
	defer ts.Close()

	params := backend.CreateParams{
		Title:          "t",
		IdempotencyKey: "key-abc",
		Force:          true,
	}
	if _, err := ab.Create(context.Background(), params); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if gotKey != "key-abc" || gotForce != "true" {
		t.Errorf("headers = key %q force %q, want key-abc/true", gotKey, gotForce)
	}

	var asMap map[string]any
	if err := json.Unmarshal(gotBody, &asMap); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	for _, k := range []string{"idempotency_key", "IdempotencyKey", "force", "Force"} {
		if _, ok := asMap[k]; ok {
			t.Errorf("body leaked field %q", k)
		}
	}
}

func TestCreate_NoHeadersWhenUnset(t *testing.T) {
	var sawKey, sawForce bool
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, sawKey = r.Header["X-Idempotency-Key"]
		_, sawForce = r.Header["X-Idempotency-Force"]
		respondOK(w, gen.IssueResponse{Id: "loom-1"})
	})
	defer ts.Close()

	if _, err := ab.Create(context.Background(), backend.CreateParams{Title: "t"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sawKey || sawForce {
		t.Errorf("unset params must not send headers (key=%v force=%v)", sawKey, sawForce)
	}
}
