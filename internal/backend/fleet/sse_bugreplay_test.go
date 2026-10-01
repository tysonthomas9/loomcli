//go:build daemon_bugreplay

package fleet

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestBugReplay626a(t *testing.T) {
	var requested []string
	fb, server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		since := r.URL.Query().Get("since")
		requested = append(requested, since)
		switch since {
		case "0":
			respondOK(w, fleetMutationsResponse{
				Events: []fleetMutationEvent{{ID: "1-0", Timestamp: time.Unix(1, 0), Action: "issue.update", EntityType: "issue", EntityID: "one"}},
				Cursor: "1-0", HasMore: true,
			})
		case "1-0":
			respondOK(w, fleetMutationsResponse{
				Events: []fleetMutationEvent{{ID: "2-0", Timestamp: time.Unix(2, 0), Action: "issue.update", EntityType: "issue", EntityID: "two"}},
				Cursor: "2-0", HasMore: false,
			})
		default:
			t.Errorf("unexpected since cursor %q", since)
			http.Error(w, "unexpected cursor", http.StatusBadRequest)
		}
	})
	defer server.Close()
	got, err := fb.GetMutationsAfter(context.Background(), "0")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Cursor != "1-0" || got[1].Cursor != "2-0" {
		t.Fatalf("paginated replay returned %v, want both durable cursor events", got)
	}
	if len(requested) != 2 || requested[0] != "0" || requested[1] != "1-0" {
		t.Fatalf("requested pages from %v, want [0 1-0]", requested)
	}
}
