package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestSetModelOptionsAloneKeepTheModel: SetModel with no model, as the
// service calls it after a resume with only a saved effort, sets the
// variant on the session's own model, else on the service default.
func TestSetModelOptionsAloneKeepTheModel(t *testing.T) {
	var sets []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/session/with", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"with","model":{"providerID":"openai","id":"gpt-5.5","variant":"medium"}}}`))
	})
	mux.HandleFunc("GET /api/session/without", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"without"}}`))
	})
	mux.HandleFunc("GET /api/model/default", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"m","providerID":"fake"}}`))
	})
	mux.HandleFunc("POST /api/session/{id}/model", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model map[string]string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		m := body.Model
		sets = append(sets, r.PathValue("id")+":"+m["providerID"]+"/"+m["id"]+"@"+m["variant"])
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewClient(srv.URL, "pw")
	high := []loomharness.Option{{ID: loomharness.OptionEffort, Value: "high"}}
	for _, id := range []string{"with", "without"} {
		if err := c.Session(loomharness.NativeRef{NativeID: id}).SetModel(context.Background(), "", high); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"with:openai/gpt-5.5@high", "without:fake/m@high"}; len(sets) != 2 || sets[0] != want[0] || sets[1] != want[1] {
		t.Fatalf("model sets %q, want %q", sets, want)
	}
}
